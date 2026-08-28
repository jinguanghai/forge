package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 1) MarshalJSON: 无图时与旧格式字节级一致 (前缀缓存兼容)
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
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte("fake-png-bytes")...)
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
	if err := os.WriteFile(filepath.Join(sub, "舌.jpg"), []byte{0xFF, 0xD8, 0xFF, 0xE0, 1}, 0644); err != nil {
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
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte("x")...)
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
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte("x")...)
	if err := os.WriteFile(filepath.Join(dir, "a.png"), png, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.jpg"), []byte{0xFF, 0xD8, 0xFF, 0xE0}, 0644); err != nil {
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
func TestDetectImagesAbsPath(t *testing.T) {
	wd, _ := os.Getwd()
	sub := filepath.Join(wd, "_chk")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	pngPath := filepath.Join(sub, "测试图.png")
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte("x")...)
	if err := os.WriteFile(pngPath, png, 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(pngPath)
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
		w.Write([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 1, 2, 3})
	})
	mux.HandleFunc("/bad.png", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>not an image</html>"))
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
}
