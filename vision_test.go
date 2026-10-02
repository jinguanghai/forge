package main

import (
	"bytes"
	"encoding/json"
	"image"
	imgcolor "image/color"
	imagejpeg "image/jpeg"
	imagepng "image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 1) MarshalJSON: 无图时与旧格式字节级一致 (前缀缓存兼容)
// ─── 测试用真图生成器 ────────────────────────────────────────────
// 历史教训 (2026-09-28): 本文件曾用 append([]byte{0x89,'P','N','G',...}, "fake-png-bytes"...)
// 伪造图片 —— "魔数对但内容垃圾"。这类字节正是当日炸会话的形态:
// 通过 magic 判定 → 内联发送 → 服务端解码失败 → HTTP 400 使整轮请求失败。
// 测试把 bug 当契约, 会挡住修复并让回归红变绿。故统一改为真编码生成。

func realPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, imgcolor.RGBA{200, 30, 30, 255})
	img.Set(1, 1, imgcolor.RGBA{30, 30, 200, 255})
	if err := imagepng.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func realJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, imgcolor.RGBA{200, 30, 30, 255})
	if err := imagejpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestLoadImagePart_RejectsBrokenImages 接线哨兵: 钉住"坏图在真实入口被拒"。
// 单测 verifyImagePayload 只能证明函数正确, 不能证明它被调用 ——
// 若只测函数, "删掉调用点"的变异体会静默逃逸。故从三个真实入口各喂一遍坏图:
// loadImagePart (底层) / detectImages (用户输入路径) / detectToolImages (工具输出路径,
// 即 2026-09-28 事故的实际触发点)。
func TestLoadImagePart_RejectsBrokenImages(t *testing.T) {
	dir := t.TempDir()
	goodPNG := realPNG(t)
	broken := []struct {
		name string
		b    []byte
	}{
		{"事故4B伪JPEG.jpg", []byte{0xFF, 0xD8, 0xFF, 0xE0}},
		{"事故22B伪PNG.png", append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte("fake-png-bytes")...)},
		{"截断真PNG.png", goodPNG[:len(goodPNG)-4]},
		{"截断真PNG半张.png", goodPNG[:len(goodPNG)/2]},
		{"文本冒充.jpg", []byte("fake-jpeg-data-not-an-image")},
	}
	for _, c := range broken {
		if err := os.WriteFile(filepath.Join(dir, c.name), c.b, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadImagePart(filepath.Join(dir, c.name)); err == nil {
			t.Errorf("%s (%d bytes): 坏图必须被 loadImagePart 拒绝, 实得通过", c.name, len(c.b))
		}
		if parts, _ := detectImages("看图 "+c.name, dir); len(parts) != 0 {
			t.Errorf("%s: 坏图不应出现在 detectImages 结果中 (parts=%d)", c.name, len(parts))
		}
		if parts := detectToolImages("文件列表: "+c.name, dir); len(parts) != 0 {
			t.Errorf("%s: 坏图不应被 detectToolImages 注入 (parts=%d)", c.name, len(parts))
		}
	}
	// 反向对照: 真图必须仍能通过全部三个入口 (防"一律拒绝"式的假绿)
	good := filepath.Join(dir, "真图.png")
	if err := os.WriteFile(good, goodPNG, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadImagePart(good); err != nil {
		t.Errorf("真图必须通过 loadImagePart: %v", err)
	}
	if parts, _ := detectImages("看图 真图.png", dir); len(parts) != 1 {
		t.Errorf("真图必须被 detectImages 检出, 实得 %d", len(parts))
	}
	if parts := detectToolImages("文件列表: 真图.png", dir); len(parts) != 1 {
		t.Errorf("真图必须被 detectToolImages 检出, 实得 %d", len(parts))
	}
}

// TestVerifyImagePayload 结构有效性校验 —— 2026-09-28 会话 400 事故回归哨兵。
// 钉住: "magic 像图但不可解码" 必须被拒; 真图必须通过; 截断图必须被拒。
func TestVerifyImagePayload(t *testing.T) {
	goodPNG, goodJPEG := realPNG(t), realJPEG(t)
	cases := []struct {
		name string
		b    []byte
		mime string
		want bool // true = 通过校验
	}{
		{"真PNG", goodPNG, "image/png", true},
		{"真JPEG", goodJPEG, "image/jpeg", true},
		{"事故形态: 4B伪JPEG", []byte{0xFF, 0xD8, 0xFF, 0xE0}, "image/jpeg", false},
		{"事故形态: 22B伪PNG", append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte("fake-png-bytes")...), "image/png", false},
		{"纯PNG魔数8B", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, "image/png", false},
		{"6B假GIF头", []byte("GIF89a"), "image/gif", false},
		{"文本冒充JPEG", []byte("fake-jpeg"), "image/jpeg", false},
		{"截断真PNG(前半)", goodPNG[:len(goodPNG)/2], "image/png", false},
		{"截断真PNG(少末4B)", goodPNG[:len(goodPNG)-4], "image/png", false},
		{"截断真JPEG(少末2B)", goodJPEG[:len(goodJPEG)-2], "image/jpeg", false},
		{"WebP过短12B", []byte{'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'E', 'B', 'P'}, "image/webp", false},
		{"WebP长度不自洽", append([]byte{'R', 'I', 'F', 'F', 12, 0, 0, 0, 'W', 'E', 'B', 'P'}, []byte("VP8 ")...), "image/webp", false},
	}
	for _, c := range cases {
		err := verifyImagePayload(c.b, c.mime)
		if c.want && err != nil {
			t.Errorf("%s: 应通过, 实得 err=%v", c.name, err)
		}
		if !c.want && err == nil {
			t.Errorf("%s: 应被拒, 实得通过 (%d bytes)", c.name, len(c.b))
		}
	}
}

func TestChatMessageMarshalNoImage(t *testing.T) {
	m := ChatMessage{Role: "user", Content: "你好"}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "image_url") || strings.Contains(s, "\"content\":[") {
		t.Fatalf("无图时不应输出多模态块: %s", s)
	}
	if !strings.Contains(s, "\"content\":\"你好\"") {
		t.Fatalf("无图 content 应为字符串: %s", s)
	}
}

// 2) MarshalJSON: 有图时 content 输出 [text + image_url...] 数组
func TestChatMessageMarshalWithImage(t *testing.T) {
	m := ChatMessage{
		Role:    "user",
		Content: "看图",
		Images:  []ImagePart{{URL: "data:image/png;" + "b" + "ase64,AAAA", Detail: "high"}},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "\"content\":[") {
		t.Fatalf("有图时 content 应为数组: %s", s)
	}
	if !strings.Contains(s, "\"type\":\"text\"") || !strings.Contains(s, "\"type\":\"image_url\"") {
		t.Fatalf("缺 text/image_url 块: %s", s)
	}
	if !strings.Contains(s, "data:image/png;") {
		t.Fatalf("缺图片 data URL: %s", s)
	}
	var wire struct {
		Content []map[string]interface{} `json:"content"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Content) != 2 {
		t.Fatalf("应有 2 块 (text+image), 实得 %d", len(wire.Content))
	}
}

// 3) estimateTokens 图片计数 (官方: 每图 ≤384)
func TestEstimateTokensImages(t *testing.T) {
	plain := estimateTokens([]ChatMessage{{Role: "user", Content: "看图"}})
	withImg := estimateTokens([]ChatMessage{{Role: "user", Content: "看图", Images: []ImagePart{{URL: "data:image/png;" + "b" + "ase64,AAAA"}}}})
	if withImg-plain != 384 {
		t.Fatalf("每图应计 384 tokens, 差值=%d", withImg-plain)
	}
}
func TestSniffImageMIME(t *testing.T) {
	cases := []struct {
		b    []byte
		want string
		ok   bool
	}{
		{[]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 1, 2, 3}, "image/png", true},
		{[]byte{0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3}, "image/jpeg", true},
		{[]byte("GIF89a" + strings.Repeat("x", 10)), "image/gif", true},
		{[]byte("GIF87a" + strings.Repeat("x", 10)), "image/gif", true},
		{[]byte("RIFF" + strings.Repeat("x", 4) + "WEBP" + strings.Repeat("x", 10)), "image/webp", true},
		{[]byte("hello world this is not an image"), "", false},
		{[]byte{}, "", false},
	}
	for i, c := range cases {
		got, ok := sniffImageMIME(c.b)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("case %d: got=%q ok=%v want=%q ok=%v", i, got, ok, c.want, c.ok)
		}
	}
}

// 5) detectImages: 中文路径/不存在/子目录
func TestDetectImages(t *testing.T) {
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "舌象.png")
	png := realPNG(t)
	if err := os.WriteFile(pngPath, png, 0644); err != nil {
		t.Fatal(err)
	}

	parts, err := detectImages("看图 舌象.png", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 {
		t.Fatalf("应检出 1 张图, 实得 %d", len(parts))
	}
	if !strings.HasPrefix(parts[0].URL, "data:image/png;") {
		t.Fatalf("URL 前缀错误: %s", parts[0].URL)
	}

	parts, err = detectImages("看图 不存在的图.png", dir)
	if err != nil || len(parts) != 0 {
		t.Fatalf("不存在路径应跳过: parts=%d err=%v", len(parts), err)
	}

	sub := filepath.Join(dir, "张阿姨")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "舌.jpg"), realJPEG(t), 0644); err != nil {
		t.Fatal(err)
	}
	parts, err = detectImages("看看 张阿姨/舌.jpg", dir)
	if err != nil || len(parts) != 1 {
		t.Fatalf("子目录图应检出: parts=%d err=%v", len(parts), err)
	}
}

// 6) 边界: 大写扩展名 (.PNG) —— (?i) 标志应覆盖
func TestDetectImagesUpperExt(t *testing.T) {
	dir := t.TempDir()
	png := realPNG(t)
	if err := os.WriteFile(filepath.Join(dir, "舌象.PNG"), png, 0644); err != nil {
		t.Fatal(err)
	}
	parts, err := detectImages("看图 舌象.PNG", dir)
	if err != nil || len(parts) != 1 {
		t.Fatalf("大写扩展名应检出 1: parts=%d err=%v", len(parts), err)
	}
}

// 7) 边界: 多图同句
func TestDetectImagesMulti(t *testing.T) {
	dir := t.TempDir()
	png := realPNG(t)
	if err := os.WriteFile(filepath.Join(dir, "a.png"), png, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.jpg"), realJPEG(t), 0644); err != nil {
		t.Fatal(err)
	}
	parts, err := detectImages("看看 a.png 和 b.jpg", dir)
	if err != nil || len(parts) != 2 {
		t.Fatalf("多图应检出 2: parts=%d err=%v", len(parts), err)
	}
}

// 8) 边界: 文件名中间带空格 —— 只匹配扩展名前无空格段, stat 失败跳过 (不崩溃)
func TestDetectImagesSpaceName(t *testing.T) {
	dir := t.TempDir()
	parts, err := detectImages("看图 张阿姨 舌象.png", dir)
	if err != nil || len(parts) != 0 {
		t.Fatalf("空格文件名应安全跳过: parts=%d err=%v", len(parts), err)
	}
}

// 9) 边界: Windows 盘符绝对路径
// 用 t.TempDir() —— 它本身即绝对路径 (Windows 盘符路径), 且测试结束自动清理。
// 旧版在仓库根建 _chk/ 后只 defer os.Remove(pngPath) 删文件、不删目录,
// 每跑一次 go test 就在工作目录留一个残留目录 (20260927 实测 _chk/ 内躺 11.8MB 旧 exe)。
// workDir 仍传 wd 且与图片所在目录不同, 保住"绝对路径不被 workDir 拼接"的区分度。
func TestDetectImagesAbsPath(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sub := t.TempDir()
	pngPath := filepath.Join(sub, "测试图.png")
	png := realPNG(t)
	if err := os.WriteFile(pngPath, png, 0644); err != nil {
		t.Fatal(err)
	}
	parts, err := detectImages("看图 "+pngPath, wd)
	if err != nil || len(parts) != 1 {
		t.Fatalf("绝对路径应检出 1: parts=%d err=%v", len(parts), err)
	}
}

//  10. 边界: http(s) URL 直接透传 (官方第二式)
//  10. 边界: http(s) URL —— 本地下载→magic 校验→base64 内联 (不再裸透传)。
//     真图 URL → 内联 data URL; 非图 URL (HTML) → 跳过不发送, 防服务端 400。
func TestDetectImagesURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		w.Write(realPNG(t))
	})
	mux.HandleFunc("/bad.png", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>not an image</html>"))
	})
	mux.HandleFunc("/broken.jpg", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0}) // 2026-09-28 事故形态: 4B 伪 JPEG
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 真图: 内联为 data URL
	parts, err := detectImages("看图 "+srv.URL+"/img.png", ".")
	if err != nil || len(parts) != 1 {
		t.Fatalf("真图 URL 应内联检出 1: parts=%d err=%v", len(parts), err)
	}
	if !strings.HasPrefix(parts[0].URL, "data:image/png;") {
		t.Fatalf("真图应内联为 data URL: %s", parts[0].URL)
	}
	// 非图: 跳过, 不触发服务端 400
	parts, err = detectImages("看图 "+srv.URL+"/bad.png", ".")
	if len(parts) != 0 {
		t.Fatalf("非图 URL 应跳过 (防 400): parts=%d err=%v", len(parts), err)
	}
	// 魔数对但内容垃圾的 URL (事故形态的远程版): 必须跳过, 不得触达服务端
	parts, err = detectImages("看图 "+srv.URL+"/broken.jpg", ".")
	if len(parts) != 0 {
		t.Fatalf("坏图 URL 应跳过 (防 400): parts=%d err=%v", len(parts), err)
	}
}
