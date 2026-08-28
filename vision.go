package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ─── 识图 (Vision) ─────────────────────────────────────────────
// 官方能力: deepseek-v4-flash-vision-exp 接受文本+图片 (2026-02 发布, API Docs
// /guides/vision)。支持 JPEG/PNG/GIF/WebP, 格式按文件内容检测 (非扩展名/MIME)。
// 传图三式: ①base64 data URL 内联 (≤48MiB 请求体, 每图 ≤32MiB) —— 本地文件用这个;
// ②公开 http(s) URL (≤8192 字符 / 32MiB / 60s 下载); ③Files API file_id (≤64MiB)。
// token 计费: 每图自动缩放后 ≤384 tokens (800×800 量级)。
//
// 集成方式 (agent.go): 用户输入含图片路径 → detectImages 读文件 → base64 data URL
// 挂到当轮 user 消息 Images 字段 → MarshalJSON 输出多模态 content 数组 → 模型路由
// 切 ModelVision。图片仅在当轮请求内存中, 不落盘 history (重放安全)。

// imagePathRe 匹配输入文本中的图片路径候选 (支持中文/反斜杠/点号/连字符)。
var imagePathRe = regexp.MustCompile(`(?i)[\w.\-\p{Han}/\\:]+\.(png|jpe?g|gif|webp)`)

// urlImageRe 匹配 http(s) 图片链接 (官方传图第二式)。
// 本地下载后 magic 校验再 base64 内联 (第一式), 不再裸透传 —— 消除模型侧
// "unsupported image" 400 (URL 常返回 HTML/JSON/重定向/损坏内容, 服务端解码失败)。
var urlImageRe = regexp.MustCompile(`(?i)https?://[\w.\-/:%?@#&=+~;,]+\.(png|jpe?g|gif|webp)`)

// detectImages 从输入提取图片路径 → 读文件 → base64 data URL。
// 本地文件按内容 magic 校验内联 (第一式); http(s) URL 经 loadImageURL 下载内联
// (第二式修正在地: 不再裸透传)。只收录存在且头部 magic 匹配的图片;
// 提取失败/不存在/URL 非图则跳过 (文本误匹配或无效链接不污染请求),
// 但"路径存在而读取失败"返回 error (真文件读不动必须报错, 不能静默丢图)。
func detectImages(input, workDir string) ([]ImagePart, error) {
	seen := make(map[string]bool)
	var parts []ImagePart
	var firstErr error
	// 第一遍: http(s) 外部图片 URL —— 本地下载→magic 校验→base64 内联。
	// 下载失败 / 非图内容 (HTML/JSON/损坏) 一律跳过不发送, 避免服务端 400。
	for _, u := range urlImageRe.FindAllString(input, -1) {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		img, err := loadImageURL(u)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("识图下载 %s: %w", u, err)
			}
			continue
		}
		parts = append(parts, img)
	}
	// 第二遍: 本地文件路径 —— 读文件 base64 内联 (官方第一式)
	for _, cand := range imagePathRe.FindAllString(input, -1) {
		cand = strings.TrimSpace(cand)
		if cand == "" || seen[cand] {
			continue
		}
		seen[cand] = true
		p := cand
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, filepath.FromSlash(cand))
		}
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue // 非文件: 普通文本里的 ".png" 字样, 跳过
		}
		img, err := loadImagePart(p)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("识图加载 %s: %w", cand, err)
			}
			continue
		}
		parts = append(parts, img)
	}
	return parts, firstErr
}

// loadImagePart 读图片文件 → magic 判定格式 → base64 data URL。
// Detail=high 保原图 (舌象/处方截图要细节; 模型侧仍自动缩放计费)。
func loadImagePart(path string) (ImagePart, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ImagePart{}, err
	}
	mime, ok := sniffImageMIME(b)
	if !ok {
		return ImagePart{}, fmt.Errorf("不支持的图片格式 (仅 JPEG/PNG/GIF/WebP) 或文件损坏")
	}
	if len(b) > 32*1024*1024 {
		return ImagePart{}, fmt.Errorf("图片 %d bytes 超 32MiB 内联上限 (大图请压缩或裁剪)", len(b))
	}
	return ImagePart{
		URL:    "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b),
		Detail: "high",
	}, nil
}

// sniffImageMIME 按文件头 magic 判定图片 MIME (官方: 按内容检测, 不信扩展名)。
// loadImageURL 下载 http(s) 图片 → 大小上限 → magic 判定格式 → base64 data URL。
// 修复: 裸透传 URL 时模型侧解码失败产生 "unsupported image" 400 (URL 可能返回
// HTML/JSON/重定向/损坏内容)。本地先行校验, 非图或下载失败返回 error 由调用方跳过。
// 15s 超时 + 32MiB 上限 (官方第二式同款约束)。仅允许 http/https。
func loadImageURL(u string) (ImagePart, error) {
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return ImagePart{}, fmt.Errorf("URL 协议不支持: %s", u)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return ImagePart{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ImagePart{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024+1))
	if err != nil {
		return ImagePart{}, err
	}
	if len(b) > 32*1024*1024 {
		return ImagePart{}, fmt.Errorf("图片 %d bytes 超 32MiB 上限", len(b))
	}
	mime, ok := sniffImageMIME(b)
	if !ok {
		return ImagePart{}, fmt.Errorf("URL 返回内容非受支持图片格式 (JPEG/PNG/GIF/WebP)")
	}
	return ImagePart{
		URL:    "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b),
		Detail: "high",
	}, nil
}

func sniffImageMIME(b []byte) (string, bool) {
	switch {
	case len(b) >= 8 && b[0] == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G':
		return "image/png", true
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg", true
	case len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a"):
		return "image/gif", true
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp", true
	default:
		return "", false
	}
}

// discoverImagesInDir 遍历目录收集全部图片文件 (按内容 magic 校验, 非扩展名)。
// 供 /看图 <目录> 命令使用: 指定一个目录 → 识别其中所有图片。
// 忽略子目录与混入的非图文件 (截图目录常含 desktop.ini 等); 目录不存在报错。
func discoverImagesInDir(dir string) ([]ImagePart, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s 不是目录", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var parts []ImagePart
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		img, err := loadImagePart(p)
		if err != nil {
			continue
		}
		parts = append(parts, img)
	}
	return parts, nil
}

// detectToolImages 从工具输出文本提取图片路径 (浏览器截图 / 数据生成图等) → base64。
// 与 detectImages 一致: 内置"文件存在 + magic 校验"过滤 (imagePathRe + loadImagePart),
// 不存在的路径 / 损坏文件 / 目录 / 非图片自动跳过 —— 避免把工具输出里的 ".png" 字样
// 误当图反喂模型。专门用于工具结果回填: 工具产出图片落盘后, 模型拿到的是文本路径,
// 需"看到"图片 (典型: browser gate screenshot 落盘 .png)。只收录本地文件, 不透传
// http URL (工具输出里的外部图 URL 一般无意义, 与用户输入场景不同)。
func detectToolImages(out, workDir string) []ImagePart {
	seen := make(map[string]bool)
	var parts []ImagePart
	for _, cand := range imagePathRe.FindAllString(out, -1) {
		cand = strings.TrimSpace(cand)
		if cand == "" || seen[cand] {
			continue
		}
		seen[cand] = true
		p := cand
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, filepath.FromSlash(cand))
		}
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		img, err := loadImagePart(p)
		if err != nil {
			continue
		}
		parts = append(parts, img)
	}
	return parts
}

// ─── DSH 式能力门控 + 图片拒绝降级 (20260827 六识图补兜底) ─────────────────
// DSH 官方经验: 模型能力元数据前置于图片准入 —— 未声明支持 visual 模态视为负能力,
// preflight 拒绝图片; 服务端偶发 "unsupported image" 400 时, 降级 text-only 重发,
// 而不是整体请求失败。forge 无模型能力查询, 以 ModelVision 配置为"支持视觉"的等价信号。

// visionCapable 判断当前是否有可用的视觉模型。ModelVision 为空 = 无视觉能力。
// 负能力门控: 无视觉能力时禁止挂图 (降级 text-only), 避免把图发给非视觉模型 → 400。
func visionCapable(cfg *Config) bool {
	return cfg != nil && strings.TrimSpace(cfg.ModelVision) != ""
}

// isImageUnsupportedError 判定错误是否为"图片不被服务端支持"类 400。
// 形态如: "LLM HTTP 400: .messages[35].image[0]: You have uploaded an unsupported image..."
// 根因 = 某张图触达模型侧后被拒 (格式/分辨率/数量/解码) —— 属于可降级错误:
// 剥离全部图片重发文本 (text-only fallback), 而非整体请求失败。
func isImageUnsupportedError(err error) bool {
	var le *LLMError
	if !errors.As(err, &le) {
		return false
	}
	if le.StatusCode != 400 {
		return false
	}
	m := strings.ToLower(le.Message)
	return strings.Contains(m, "image") && (strings.Contains(m, "unsupported") || strings.Contains(m, ".image[") || strings.Contains(m, ".images["))
}
