package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"encoding/gob"
)

// ─── 缺陷 #5 固化: cacheResult 重复键产生幽灵条目 ────────────────────
//
// 旧实现只做 cache[key]=r + append(cacheKeys,key)，同一 key 被写多次时 map 覆盖写
// 只留一条、cacheKeys 却追加一条 → 队列与 map 失同步。后果:
//   ①幽灵条目让 FIFO 淘汰空转（delete 一个已不在 map 的 key，len(cache) 不降）
//   ②len(cache) 突破 cacheMaxSize（实测并发后 16 → 17）
//   ③有效容量被侵蚀（实测 4 个 key 各写 2 次 → cacheKeys 涨到 7 项）
// 触发路径是真实存在的: TTL(600s) 过期后重算、retryGate 重试成功回写。

// assertCacheConsistent 校验 cache 与 cacheKeys 一一对应、无重复、不超上限。
func assertCacheConsistent(t *testing.T, f *Forge) {
	t.Helper()
	f.cacheMu.RLock()
	defer f.cacheMu.RUnlock()
	seen := make(map[string]bool, len(f.cacheKeys))
	for _, k := range f.cacheKeys {
		if seen[k] {
			t.Fatalf("cacheKeys 含重复项 %q（队列 %d 项，map %d 条）", k, len(f.cacheKeys), len(f.cache))
		}
		seen[k] = true
		if _, ok := f.cache[k]; !ok {
			t.Fatalf("cacheKeys 含幽灵项 %q（不在 cache map 中）", k)
		}
	}
	if len(f.cache) != len(f.cacheKeys) {
		t.Fatalf("cache(%d 条) 与 cacheKeys(%d 项) 数量不一致", len(f.cache), len(f.cacheKeys))
	}
	if f.cacheMaxSize > 0 && len(f.cache) > f.cacheMaxSize {
		t.Fatalf("突破上限: cache=%d > maxSize=%d", len(f.cache), f.cacheMaxSize)
	}
}

func TestCacheResult_SameKeyRepeated(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	for i := 0; i < 3; i++ {
		f.cacheResult("k1", ForgeGateResult{OK: true, Lang: "python"})
	}
	assertCacheConsistent(t, f)
	f.cacheMu.RLock()
	n, nk := len(f.cache), len(f.cacheKeys)
	f.cacheMu.RUnlock()
	if n != 1 || nk != 1 {
		t.Fatalf("同一 key 写 3 次: cache=%d cacheKeys=%d, want 1/1", n, nk)
	}
}

// 有效容量不被侵蚀: 4 个不同 key 各写 2 次后，仍应能容纳 maxSize 条不同 key。
func TestCacheResult_EffectiveCapacityNotEroded(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	f.cacheMaxSize = 4
	for i := 0; i < 4; i++ {
		k := fmt.Sprintf("k%d", i)
		f.cacheResult(k, ForgeGateResult{OK: true})
		f.cacheResult(k, ForgeGateResult{OK: true})
	}
	assertCacheConsistent(t, f)
	f.cacheMu.RLock()
	n, nk := len(f.cache), len(f.cacheKeys)
	f.cacheMu.RUnlock()
	if n != 4 || nk != 4 {
		t.Fatalf("4 key 各写 2 次后: cache=%d cacheKeys=%d, want 4/4（旧实现为 3/7）", n, nk)
	}
	// 再写满一轮不同 key: 容量应保持 4
	for i := 4; i < 12; i++ {
		f.cacheResult(fmt.Sprintf("k%d", i), ForgeGateResult{OK: true})
	}
	assertCacheConsistent(t, f)
}

// FIFO 语义: 淘汰最旧的；重复写同一 key 视为刷新（移除旧位置后追加到队尾）。
func TestCacheResult_FIFOOrder(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	f.cacheMaxSize = 3
	f.cacheResult("a", ForgeGateResult{OK: true})
	f.cacheResult("b", ForgeGateResult{OK: true})
	f.cacheResult("c", ForgeGateResult{OK: true})
	f.cacheResult("a", ForgeGateResult{OK: true}) // 刷新 a → 队尾
	f.cacheResult("d", ForgeGateResult{OK: true}) // 淘汰 b
	assertCacheConsistent(t, f)
	f.cacheMu.RLock()
	defer f.cacheMu.RUnlock()
	for _, k := range []string{"a", "c", "d"} {
		if _, ok := f.cache[k]; !ok {
			t.Errorf("FIFO 淘汰错误: %q 不应被淘汰（cacheKeys=%v）", k, f.cacheKeys)
		}
	}
	if _, ok := f.cache["b"]; ok {
		t.Errorf("b 应被淘汰，cacheKeys=%v", f.cacheKeys)
	}
}

// 并发写: 无重复、无幽灵、不超上限（旧实现实测 cache=17 > maxSize=16）。
func TestCacheResult_ConcurrentNoOverflow(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	f.cacheMaxSize = 16
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				f.cacheResult(fmt.Sprintf("key%d", (g*7+i)%40), ForgeGateResult{OK: true})
			}
		}(g)
	}
	wg.Wait()
	assertCacheConsistent(t, f)
}

func TestRemoveCacheKeyLocked_ClearsAllDuplicates(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	// 手工构造带毒的队列（模拟旧版本写出的状态）
	f.cacheMu.Lock()
	f.cache = map[string]ForgeGateResult{"a": {OK: true}, "b": {OK: true}}
	f.cacheKeys = []string{"a", "a", "b", "a"}
	f.removeCacheKeyLocked("a")
	f.cacheMu.Unlock()
	assertCacheConsistent(t, f)
	f.cacheMu.RLock()
	defer f.cacheMu.RUnlock()
	if len(f.cacheKeys) != 1 || f.cacheKeys[0] != "b" {
		t.Fatalf("cacheKeys = %v, want [b]", f.cacheKeys)
	}
}

func TestRemoveCacheKeyLocked_EmptyQueue(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	f.cacheMu.Lock()
	f.cache = map[string]ForgeGateResult{}
	f.cacheKeys = nil
	f.removeCacheKeyLocked("nope") // 不得 panic
	f.cacheMu.Unlock()
	assertCacheConsistent(t, f)
}

// 持久化归一化: 旧版本写出的文件可能带重复/幽灵键，加载时必须清理，
// 否则 FIFO 淘汰空转、有效容量被侵蚀（且这是跨版本升级的真实路径）。
func TestLoadCacheFromDisk_NormalizesGhosts(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	f.cacheMaxSize = 100
	f.cachePersistFile = filepath.Join(t.TempDir(), "forge_cache.gob")
	file, err := os.Create(f.cachePersistFile)
	if err != nil {
		t.Fatal(err)
	}
	enc := gob.NewEncoder(file)
	err = enc.Encode(cachePersistFormat{
		Entries: map[string]ForgeGateResult{"a": {OK: true}, "b": {OK: true}},
		Keys:    []string{"a", "a", "ghost", "b", "b"}, // 重复 + 幽灵
	})
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	f.loadCacheFromDisk()
	assertCacheConsistent(t, f)
	f.cacheMu.RLock()
	defer f.cacheMu.RUnlock()
	if len(f.cacheKeys) != 2 {
		t.Fatalf("归一化后 cacheKeys = %v, want [a b]", f.cacheKeys)
	}
}

// 旧格式兼容: 早期版本写的是裸 map（无 Keys 字段），解码失败后须回退重解，
// 且回退路径也要保持 cache/cacheKeys 一致。这是跨版本升级的真实路径。
func TestLoadCacheFromDisk_OldFormat(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	f.cacheMaxSize = 100
	f.cachePersistFile = filepath.Join(t.TempDir(), "forge_cache.gob")
	file, err := os.Create(f.cachePersistFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := gob.NewEncoder(file).Encode(map[string]ForgeGateResult{"old": {OK: true}}); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	f.loadCacheFromDisk()
	assertCacheConsistent(t, f)
	f.cacheMu.RLock()
	defer f.cacheMu.RUnlock()
	if len(f.cache) != 1 {
		t.Fatalf("旧格式加载后 cache=%d 条, want 1", len(f.cache))
	}
	if len(f.cacheKeys) != 1 || f.cacheKeys[0] != "old" {
		t.Fatalf("旧格式加载后 cacheKeys=%v, want [old]", f.cacheKeys)
	}
}

// 损坏文件: 两种解码都失败 → 保持空缓存, 不 panic、不污染。
func TestLoadCacheFromDisk_CorruptFile(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	f.cachePersistFile = filepath.Join(t.TempDir(), "forge_cache.gob")
	if err := os.WriteFile(f.cachePersistFile, []byte("not a gob stream at all"), 0644); err != nil {
		t.Fatal(err)
	}
	f.loadCacheFromDisk() // 不得 panic
	assertCacheConsistent(t, f)
	f.cacheMu.RLock()
	defer f.cacheMu.RUnlock()
	if len(f.cache) != 0 || len(f.cacheKeys) != 0 {
		t.Fatalf("损坏文件应保持空缓存, 实际 cache=%d keys=%d", len(f.cache), len(f.cacheKeys))
	}
}

// ─── Shutdown 必须等在飞的异步落盘排空 ────────────────────────────────
//
// 缺陷实测(20260925): cacheResult 每 50 次触发一次 `go saveCacheToDisk()`,
// 而 Shutdown 不等它排空就返回, 于是异步写与调用方的后续动作竞态:
//
//	· 测试侧: t.TempDir 清理报 unlinkat "...The directory is not empty"
//	  (goroutine 在 RemoveAll 遍历期间把 .tmp 写回目录)。实测 -count=100
//	  复现 4 次; 同用例在 Shutdown 后 sleep 200ms 即 100/100 通过 ——
//	  根因确证为"Shutdown 返回后仍有 goroutine 在飞"。
//	· 生产侧: 进程退出时缓存可能未落盘(下次冷启动全量 miss, 与"退出前存缓存"
//	  的设计意图直接冲突), 或被更早的快照覆盖。
//
// 判据: Shutdown 返回后立即读盘, 必须解出完整快照, 且无 .tmp 残留。
func TestShutdown_DrainsPendingDiskWrites(t *testing.T) {
	f := newTestForge(t)
	f.cacheMaxSize = 64
	// 8×200 次写 → 触发 32 次异步落盘, 制造足够多的在飞 goroutine
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				f.cacheResult(fmt.Sprintf("k%d", (g*7+i)%40), ForgeGateResult{OK: true})
			}
		}(g)
	}
	wg.Wait()
	f.Shutdown() // 内部必须等所有异步落盘排空

	if _, err := os.Stat(f.cachePersistFile + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("Shutdown 后仍有 .tmp 残留 —— 异步落盘未排空 (err=%v)", err)
	}
	file, err := os.Open(f.cachePersistFile)
	if err != nil {
		t.Fatalf("Shutdown 后缓存文件缺失 —— 未落盘: %v", err)
	}
	defer file.Close()
	var data cachePersistFormat
	if err := gob.NewDecoder(file).Decode(&data); err != nil {
		t.Fatalf("Shutdown 后落盘内容不可解码: %v", err)
	}
	if len(data.Entries) == 0 {
		t.Error("Shutdown 后落盘快照为空")
	}
	f.cacheMu.RLock()
	want := len(f.cache)
	f.cacheMu.RUnlock()
	if len(data.Entries) != want {
		t.Errorf("落盘 %d 条 != 内存 %d 条 —— 旧快照覆盖了新快照", len(data.Entries), want)
	}
}

// TestShutdownWaitsForDiskWrites 结构哨兵: (*Forge).Shutdown 函数体必须含
// cacheDiskWG.Wait()。
// 为何要它: 上面的行为测试对"不等排空"只有概率性检出(需 ≥50 次 cacheResult
// 才触发异步写, 且要撞上 TempDir 清理窗口); 而这条 Wait 是消除竞态的充分条件,
// 删掉它必须立刻红。扫全包 —— Shutdown 可能被搬到任何文件。
//
// 变异实证(20260925):
//
//	· Wait() → `_ = cacheDiskWG.Wait`(编译通过、不再等待): 本哨兵 -count=20 全 20 次报警;
//	· 移除 Add(1)/Done(): 上面的行为测试 -count=20 未报警(竞态窗口未撞上)。
//	结论: 行为测试只是概率性回归哨兵, 结构哨兵才是确定性判据, 两者都要留。
func TestShutdownWaitsForDiskWrites(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	foundFunc, foundWait := false, false
	fset := token.NewFileSet()
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s: %v", n, err)
		}
		ast.Inspect(af, func(node ast.Node) bool {
			fd, ok := node.(*ast.FuncDecl)
			if !ok || fd.Name.Name != "Shutdown" || fd.Recv == nil || fd.Body == nil {
				return true
			}
			foundFunc = true
			ast.Inspect(fd.Body, func(n2 ast.Node) bool {
				call, ok := n2.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Wait" {
					return true
				}
				// f.cacheDiskWG.Wait() 的 Fun 是 SelectorExpr{X: SelectorExpr{X: f, Sel: cacheDiskWG}, Sel: Wait}
				// —— sel.X 是 SelectorExpr 而非 Ident, 按 Ident 判会永远抓不到(首版即踩此坑)。
				if sx, ok := sel.X.(*ast.SelectorExpr); ok {
					if sx.Sel != nil && sx.Sel.Name == "cacheDiskWG" { // SelectorExpr.Sel 是 *ast.Ident
						foundWait = true
					}
				}
				return true
			})
			return true
		})
	}
	if !foundFunc {
		t.Fatal("全包找不到 (*Forge).Shutdown —— 函数被删或改名")
	}
	if !foundWait {
		t.Error("(*Forge).Shutdown 缺 cacheDiskWG.Wait() —— 异步落盘未排空, " +
			"退出时缓存可能未落盘(下次冷启动全量 miss), 且与调用方后续动作竞态")
	}
}
