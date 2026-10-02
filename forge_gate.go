package main

import (
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

// saveCacheToDisk persists the in-memory cache to disk using gob encoding.
// cachePersistFormat is the on-disk format for the forge cache.
// It preserves both the map and the FIFO ordering of keys.
type cachePersistFormat struct {
	Entries map[string]ForgeGateResult
	Keys    []string // FIFO insertion order
}

var 铸剑炉_GATES = []string{
	"python", "go", "node", "math", "logic",
	"regex", "knowledge", "tcm", "browser", "chain", "self", "relation", "media",
}

// herbPairInputRE 识别 "查药对 X Y" 式中文输入（药对/双药/配伍/同现）
var herbPairInputRE = regexp.MustCompile(`(?:查药对|药对|双药|同现药对|配伍)\s*[:：]?\s*([\p{Han}]{1,8})\s*[、,+和与及\s]\s*([\p{Han}]{1,8})`)

// mustHaveOutputGates: 纯判定型 gate(按 baseLang 计), 必须有判定输出。
// 空 stdout 对它们等于「判定缺席」, 不可当成功 —— 否则 gate 二进制异常会被静默吞掉。
var mustHaveOutputGates = map[string]bool{"math": true, "logic": true, "regex": true, "relation": true, "media": true}

// cacheHitNote 生成缓存命中提示(未命中返回空串)。
//
// 动机: 命中路径静默返回 cached, 用户看到「成功 (3ms)」无法区分真跑与复用 ——
// 对 browser(实时网页)/python(可能写文件) 这类非幂等 gate, 误判会导出
// 「刚才跑过了」的错误结论。DSec 对幂等/非幂等操作显式区分; 此处取轻量版:
// 不改缓存策略(关缓存会摧毁 98% 命中率), 只把「命中」这一事实显式标注。
//
// 判据: cacheResult 在写入时给副本设 CachedAt; 正常执行路径返回的 result
// CachedAt==0, 只有命中路径返回的 cached 带非零值 —— 两条路径天然可分。
func cacheHitNote(r ForgeGateResult) string {
	if r.CachedAt <= 0 {
		return ""
	}
	age := time.Now().Unix() - r.CachedAt
	if age < 0 {
		age = 0
	}
	return fmt.Sprintf("🕒 缓存命中 · %ds 前的结果 · 本次未实际执行", age)
}

func (f *Forge) forgeGateSkipCache(code, lang, input string, skipCache bool) ForgeGateResult {
	cacheKey := f.cacheKey(code, lang, input)
	if !skipCache {
		f.cacheMu.RLock()
		if cached, ok := f.cache[cacheKey]; ok {
			// TTL invalidation: stale entries (and old-format entries with
			// CachedAt==0) are treated as misses and re-executed, so the agent
			// never serves long-outdated results for stateful code.
			// 慢 gate 长 TTL: knowledge/browser/tcm 外部延迟高,
			// 600s TTL 复用率低 → 1800s; 其余保持 600s。
			cacheTTLSeconds := cacheTTLForLang(lang)
			if time.Now().Unix()-cached.CachedAt > int64(cacheTTLSeconds) {
				f.cacheMu.RUnlock()
				f.cacheMu.Lock()
				f.removeCacheKeyLocked(cacheKey)
				f.cacheMu.Unlock()
			} else {
				f.cacheMu.RUnlock()
				f.statCacheHits.Add(1)
				slog.Debug("forge cache hit", "key", cacheKey)
				return cached
			}
		} else {
			f.cacheMu.RUnlock()
		}
	}
	start := time.Now()
	codeSize := len(code)
	codeLines := len(strings.Split(strings.TrimSpace(code), "\n"))
	compiler, ok := 铸剑炉_COMPILERS[lang]
	if !ok {
		detected := forgeDetectLang(code, "python")
		if detected != lang {
			slog.Info("forge lang fallback", "requested", lang, "detected", detected)
			lang = detected
			compiler, ok = 铸剑炉_COMPILERS[lang]
		}
		if !ok {
			// 不可达兜底: forgeDetectLang 的返回值域 {go,sh,python,node,logic,math} 与
			// hint 入参(调用点仅传 ""/"python")全部落在编译器表内, 故此处当前不可达。
			// 不变量由 TestForgeDetectLang_ReturnsOnlyCompilerTableKeys 前置拦截 ——
			// 一旦 forgeDetectLang 新增表外返回值, 该哨兵立即红, 提示本分支变可达。
			result := ForgeGateResult{
				OK: false, Lang: lang, Stage: "compile",
				Error:    fmt.Sprintf("不支持的语言: %s。支持的语言: %s", lang, f.supportedLangs()),
				Duration: time.Since(start).Milliseconds(),
				CodeSize: codeSize, CodeLines: codeLines,
				EnvFailure: true, // 换语言重跑有意义: 目标语言本身不可用
			}
			if !skipCache {
				f.cacheResult(cacheKey, result)
			}
			return result
		}
	}
	if strings.TrimSpace(code) == "" {
		result := ForgeGateResult{
			OK: false, Lang: lang, Stage: "compile",
			Error: "代码为空", Duration: time.Since(start).Milliseconds(),
			CodeSize: codeSize, CodeLines: codeLines,
		}
		if !skipCache {
			f.cacheResult(cacheKey, result)
		}
		return result
	}

	var result ForgeGateResult
	f.statBuilds.Add(1)
	if compiler.SelfHosted {
		result = f.forgeGateSelfHosted(code, lang, compiler, input, start)
	} else if compiler.InlineCode {
		result = f.forgeGateInline(code, lang, compiler, input, start)
	} else {
		result = f.forgeGateFile(code, lang, compiler, input, start)
	}
	result.CodeSize = codeSize
	result.CodeLines = codeLines
	result.Summary = summarizeOutput(result.Stdout, result.Stderr)
	if !result.OK {
		f.statErrors.Add(1)
	}

	// Wire up the CompilerError subsystem: on failure, parse stderr into
	// structured diagnostics (JSON) so callers get precise error locations.
	if !result.OK && result.Stderr != "" {
		if diag := compilerErrorsToJSON(parseCompilerError(result.Lang, result.Stderr)); diag != "" {
			result.Diagnostics = diag
		}
	}

	// Only cache successful results or permanent failures.
	// Transient failures (timeouts, tool-not-found) are NOT cached
	// so they can be retried on next invocation.
	if !skipCache && (result.OK || !isTransientError(result)) {
		f.cacheResult(cacheKey, result)
	}
	return result
}

func (f *Forge) forgeGate(code, lang, input string) ForgeGateResult {
	return f.forgeGateSkipCache(code, lang, input, false)
}

func (f *Forge) cacheKey(code, lang, input string) string {
	h := sha256.New()
	h.Write([]byte(code))
	h.Write([]byte{0})
	h.Write([]byte(lang))
	h.Write([]byte{0})
	h.Write([]byte(input))
	return hex.EncodeToString(h.Sum(nil))
}

// removeCacheKeyLocked 从缓存 map 与 FIFO 队列中一并移除 key（须持 cacheMu）。
//
// 用全量过滤而非单次删除: 队列里的重复项（历史版本写出的幽灵条目）要一次清干净,
// 否则 FIFO 淘汰会空转——delete 一个已不在 map 里的 key, len(cache) 不降, 容量被侵蚀。
func (f *Forge) removeCacheKeyLocked(key string) {
	delete(f.cache, key)
	if len(f.cacheKeys) == 0 {
		return
	}
	kept := f.cacheKeys[:0]
	for _, k := range f.cacheKeys {
		if k != key {
			kept = append(kept, k)
		}
	}
	f.cacheKeys = kept
}

func (f *Forge) cacheResult(key string, r ForgeGateResult) {
	f.cacheMu.Lock()
	defer f.cacheMu.Unlock()
	// 同一 key 重写（TTL 过期重算 / 重试成功回写）必须先移除旧位置: 否则 map 被覆盖写
	// 只留一条、cacheKeys 却追加一条 → 队列与 map 失同步。实测 40 个 key 各写 2 次
	// 后 cacheKeys 涨到 106 项，且 len(cache) 突破 cacheMaxSize（16 → 17）。
	f.removeCacheKeyLocked(key)
	// FIFO 淘汰: 循环直到腾出空位——队列里的幽灵项可能使单次淘汰无效。
	if f.cacheMaxSize > 0 {
		for len(f.cache) >= f.cacheMaxSize && len(f.cacheKeys) > 0 {
			oldest := f.cacheKeys[0]
			f.cacheKeys = f.cacheKeys[1:]
			delete(f.cache, oldest)
		}
	}
	r.CachedAt = time.Now().Unix()
	f.cache[key] = r
	f.cacheKeys = append(f.cacheKeys, key)
	f.cacheSaveCounter++
	if f.cacheSaveCounter%50 == 0 && !f.cacheDiskClosed {
		f.cacheDiskWG.Add(1)
		go func() {
			defer f.cacheDiskWG.Done()
			f.saveCacheToDisk()
		}()
	}
}

func (f *Forge) supportedLangs() string {
	langs := make([]string, 0, len(铸剑炉_COMPILERS))
	for k := range 铸剑炉_COMPILERS {
		langs = append(langs, k)
	}
	slices.Sort(langs)
	return strings.Join(langs, ", ")
}

func (f *Forge) saveCacheToDisk() {
	if f.cachePersistFile == "" {
		return
	}
	f.cacheDiskMu.Lock()
	defer f.cacheDiskMu.Unlock()
	f.cacheMu.RLock()
	// Copy under lock to minimize lock time
	data := cachePersistFormat{
		Entries: make(map[string]ForgeGateResult, len(f.cache)),
		Keys:    make([]string, len(f.cacheKeys)),
	}
	for k, v := range f.cache {
		data.Entries[k] = v
	}
	copy(data.Keys, f.cacheKeys)
	f.cacheMu.RUnlock()

	// 原子写: 先写 .tmp 再 rename, 避免崩溃/升级强杀时截断主缓存文件
	tmp := f.cachePersistFile + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		slog.Warn("forge cache save failed", "error", err)
		return
	}
	enc := gob.NewEncoder(file)
	if err := enc.Encode(data); err != nil {
		file.Close()
		os.Remove(tmp)
		slog.Warn("forge cache encode failed", "error", err)
		return
	}
	if err := file.Close(); err != nil {
		os.Remove(tmp)
		slog.Warn("forge cache close failed", "error", err)
		return
	}
	if err := os.Rename(tmp, f.cachePersistFile); err != nil {
		os.Remove(tmp)
		slog.Warn("forge cache rename failed", "error", err)
		return
	}
	slog.Debug("forge cache saved to disk", "entries", len(data.Entries))
}

// loadCacheFromDisk restores the cache from a gob-encoded file on startup.
func (f *Forge) loadCacheFromDisk() {
	if f.cachePersistFile == "" {
		return
	}
	file, err := os.Open(f.cachePersistFile)
	if err != nil {
		return // file doesn't exist yet, that's fine
	}
	defer file.Close()
	dec := gob.NewDecoder(file)

	// Try new format first (cachePersistFormat), fall back to old format (bare map)
	var data cachePersistFormat
	if err := dec.Decode(&data); err != nil {
		// Old format: bare map[string]ForgeGateResult
		file.Seek(0, 0)
		dec = gob.NewDecoder(file)
		var loaded map[string]ForgeGateResult
		if err2 := dec.Decode(&loaded); err2 != nil {
			slog.Warn("forge cache load failed, starting fresh", "error", err2)
			return
		}
		data.Entries = loaded
		data.Keys = make([]string, 0, len(loaded))
		for k := range loaded {
			data.Keys = append(data.Keys, k)
		}
	}
	f.cacheMu.Lock()
	f.cache = data.Entries
	f.cacheKeys = data.Keys
	// 归一化: 过滤掉与 map 不一致的幽灵项与重复项。旧版本写出的持久化文件可能带毒
	// （cacheKeys 有重复而 map 只有一条），不清理则 FIFO 淘汰空转、有效容量被侵蚀。
	seen := make(map[string]struct{}, len(f.cacheKeys))
	norm := make([]string, 0, len(f.cacheKeys))
	for _, k := range f.cacheKeys {
		if _, dup := seen[k]; dup {
			continue
		}
		if _, ok := f.cache[k]; !ok {
			continue
		}
		seen[k] = struct{}{}
		norm = append(norm, k)
	}
	f.cacheKeys = norm
	// Enforce cacheMaxSize: truncate the restored cache to the configured limit
	// (keep the most recent entries per FIFO order).
	if f.cacheMaxSize > 0 && len(f.cacheKeys) > f.cacheMaxSize {
		keep := f.cacheKeys[len(f.cacheKeys)-f.cacheMaxSize:]
		f.cacheKeys = keep
		trimmed := make(map[string]ForgeGateResult, len(keep))
		for _, k := range keep {
			if v, ok := f.cache[k]; ok {
				trimmed[k] = v
			}
		}
		f.cache = trimmed
	}
	f.cacheMu.Unlock()
	slog.Info("forge cache loaded from disk", "entries", len(f.cache))
}

func (f *Forge) formatResult(r ForgeGateResult) string {
	var sb strings.Builder
	emoji := langEmoji(r.Lang)
	langLabel := strings.ToUpper(r.Lang)

	// Build body first, truncate, then prepend header (preserves header budget)
	fullBody := ""
	if r.Stdout != "" {
		fullBody = r.Stdout
	}
	if r.Stderr != "" {
		if fullBody != "" {
			fullBody += "\n--- stderr ---\n"
		}
		fullBody += r.Stderr
	}
	if r.Error != "" && r.Stderr == "" {
		if fullBody != "" {
			fullBody += "\n--- error ---\n"
		}
		fullBody += r.Error
		if r.Lint != "" {
			if fullBody != "" {
				fullBody += "\n--- lint ---\n"
			}
			fullBody += r.Lint
		}
	}
	if r.Diagnostics != "" {
		if fullBody != "" {
			fullBody += "\n"
		}
		fullBody += "--- diagnostics ---\n" + r.Diagnostics
	}
	truncatedBody := truncateOutput(fullBody)
	// 缓存命中标注放最前: 它是「这份结果不是本次跑出来的」这一事实,
	// 比正文更该先被看到(非幂等 gate 误判会导出错误结论)。
	if note := cacheHitNote(r); note != "" {
		sb.WriteString(note)
		sb.WriteString("\n")
	}
	if r.OK && r.ExitCode != 0 {
		// OK=true 但退出码非零: 程序确实跑起来了且有输出, 属「结果」而非 gate 故障
		// (见 forgeGateFile 的 "Runtime errors with output are results, not gate failures")。
		// 但退出码非零必须显式呈现: 只说「成功」会被读成「运行正常」——
		// 结构体里 OK/ExitCode/Error 三重并存, 渲染层却把 ExitCode 丢了,
		// 模型据「成功」二字下结论 = 证据在呈现层被削弱。
		sb.WriteString(fmt.Sprintf("%s --- %s 成功但退出码非零 (exit=%d, %dms, %d lines) ---\n",
			emoji, langLabel, r.ExitCode, r.Duration, r.CodeLines))
	} else if r.OK {
		sb.WriteString(fmt.Sprintf("%s --- %s 成功 (%dms, %d lines) ---\n",
			emoji, langLabel, r.Duration, r.CodeLines))
	} else {
		sb.WriteString(fmt.Sprintf("%s --- %s 失败 (exit=%d, %dms, stage=%s) ---\n",
			emoji, langLabel, r.ExitCode, r.Duration, r.Stage))
	}
	sb.WriteString(truncatedBody)
	// 捕获层丢弃提示放正文之后: 它是关于上面这段正文的说明。
	if note := captureNote(r); note != "" {
		sb.WriteString("\n")
		sb.WriteString(note)
	}
	if r.Summary != "" {
		summaryText := truncateOutput(r.Summary)
		sb.WriteString("\n--- summary ---\n")
		sb.WriteString(summaryText)
	}
	return sb.String()
}
