//go:build windows

// tts.go — 铸剑炉语音输出: 文本→edge-tts 合成 mp3→winmm MCI 播放。

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var voiceMu sync.Mutex
var voiceEnabled bool = voiceDefaultEnabled()
var voiceName string = voiceDefaultName()

func voiceDefaultEnabled() bool {
	v := strings.TrimSpace(os.Getenv("FORGE_VOICE"))
	if v == "0" || strings.EqualFold(v, "off") || strings.EqualFold(v, "false") || strings.EqualFold(v, "no") {
		return false
	}
	return true
}

func voiceDefaultName() string {
	if v := strings.TrimSpace(os.Getenv("FORGE_VOICE_NAME")); v != "" {
		return v
	}
	return "zh-CN-XiaoxiaoNeural"
}

var knownVoices = []string{
	"zh-CN-XiaoxiaoNeural",
	"zh-CN-XiaoyiNeural",
	"zh-CN-XiaochenNeural",
	"zh-CN-XiaohanNeural",
	"zh-CN-XiaomengNeural",
	"zh-CN-XiaomoNeural",
	"zh-CN-XiaoruiNeural",
	"zh-CN-XiaoshuangNeural",
	"zh-CN-XiaoyanNeural",
	"zh-CN-XiaoyouNeural",
	"zh-CN-XiaozhenNeural",
	"zh-TW-HsiaoChenNeural",
	"zh-TW-HsiaoYuNeural",
	"zh-HK-HiuGaaiNeural",
}

var spAnsiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
var spCodeBlockRe = regexp.MustCompile("(?s)```.*?```")
var spInlineCodeRe = regexp.MustCompile("`[^`]*`")
var spBoldRe = regexp.MustCompile(`(\*\*|__)(.*?)(\*\*|__)`)
var spHeadRe = regexp.MustCompile("(?m)^\\s*#{1,6}\\s*")
var spListRe = regexp.MustCompile("(?m)^\\s*[-*+]\\s+")
var spQuoteRe = regexp.MustCompile("(?m)^\\s*>\\s?")
var spTableRe = regexp.MustCompile("(?m)^\\s*\\|[^\\n]*\\|\\s*$")
var spUrlRe = regexp.MustCompile(`https?:\/\/\S+`)
var spMultiSpaceRe = regexp.MustCompile(`[ \t]{2,}`)
var spMultiNewlineRe = regexp.MustCompile(`\n{3,}`)

func sanitizeForSpeech(s string) string {
	s = spAnsiRe.ReplaceAllString(s, "")
	s = spCodeBlockRe.ReplaceAllString(s, " ")
	s = spInlineCodeRe.ReplaceAllString(s, " ")
	s = spBoldRe.ReplaceAllString(s, "$2")
	s = spHeadRe.ReplaceAllString(s, "")
	s = spListRe.ReplaceAllString(s, "")
	s = spQuoteRe.ReplaceAllString(s, "")
	s = spTableRe.ReplaceAllString(s, " ")
	s = spUrlRe.ReplaceAllString(s, "")
	s = spMultiSpaceRe.ReplaceAllString(s, " ")
	s = spMultiNewlineRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

var winmm = syscall.NewLazyDLL("winmm.dll")
var procMci = winmm.NewProc("mciSendStringW")

func mciCmd(cmd string) error {
	p, err := syscall.UTF16PtrFromString(cmd)
	if err != nil {
		return err
	}
	var buf [512]uint16
	r, _, _ := procMci.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
	if r != 0 {
		return fmt.Errorf("MCI %d: %s", int(r), strings.TrimRight(syscall.UTF16ToString(buf[:]), "\x00"))
	}
	return nil
}

func playMp3(path string) error {
	_ = mciCmd("close vplay")
	cmd := `open "` + path + `" type mpegvideo alias vplay`
	if err := mciCmd(cmd); err != nil {
		return err
	}
	return mciCmd("play vplay")
}

func synthSpeech(text, voice, out string) error {
	env := []string{}
	for _, e := range os.Environ() {
		if strings.Contains(strings.ToUpper(e), "PROXY") {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")

	textFile := filepath.Join(os.TempDir(), "forge_voice_text.txt")
	if err := os.WriteFile(textFile, []byte(strings.TrimSpace(text)), 0644); err != nil {
		return err
	}
	defer os.Remove(textFile)

	script := `import sys, asyncio, edge_tts
async def main():
    text = open(sys.argv[1], encoding='utf-8').read()
    voice = sys.argv[2]
    out = sys.argv[3]
    tts = edge_tts.Communicate(text, voice)
    await tts.save(out)
asyncio.run(main())
`
	scriptFile := filepath.Join(os.TempDir(), "forge_voice_tts.py")
	if err := os.WriteFile(scriptFile, []byte(script), 0644); err != nil {
		return err
	}
	defer os.Remove(scriptFile)

	cmd := exec.Command("python", scriptFile, textFile, voice, out)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("edge-tts: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

const maxSpeechRunes = 480

func voiceSpeak(text string) error {
	voiceMu.Lock()
	en := voiceEnabled
	vName := voiceName
	voiceMu.Unlock()
	if !en {
		return nil
	}
	text = sanitizeForSpeech(text)
	runes := []rune(text)
	if len(runes) < 4 {
		return nil
	}
	if len(runes) > maxSpeechRunes {
		runes = runes[:maxSpeechRunes]
		text = string(runes) + "……(省略)"
	}
	out := filepath.Join(os.TempDir(), "forge_voice_tmp.mp3")
	_ = os.Remove(out)

	if err := synthSpeech(text, vName, out); err != nil {
		return err
	}
	if err := playMp3(out); err != nil {
		return err
	}
	go func() {
		estSec := 1.5 + float64(len([]rune(text)))*0.22
		time.Sleep(time.Duration(estSec * float64(time.Second)))
		_ = mciCmd("close vplay")
		_ = os.Remove(out)
	}()
	return nil
}

func speakLastReply(agent *AgentRunner) {
	voiceMu.Lock()
	en := voiceEnabled
	voiceMu.Unlock()
	if !en {
		return
	}
	var last string
	for i := len(agent.history) - 1; i >= 0; i-- {
		msg := agent.history[i]
		if msg.Role == "assistant" && strings.TrimSpace(msg.Content) != "" {
			last = msg.Content
			break
		}
	}
	last = sanitizeForSpeech(last)
	if len([]rune(last)) < 4 {
		return
	}
	if err := voiceSpeak(last); err != nil {
		fmt.Fprintf(os.Stderr, "\n%s 语音播报失败: %v (可 /voice off 关闭)\n", color(ansi.dim, "🔇"), err)
	}
}

func cmdVoice(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	if len(parts) <= 1 {
		voiceMu.Lock()
		st := "开"
		if !voiceEnabled {
			st = "关"
		}
		vn := voiceName
		voiceMu.Unlock()
		fmt.Printf("  %s 语音播报状态: %s (%s)\n", bold("🔊"), st, vn)
		fmt.Println("  用法:")
		fmt.Println("    /voice on|off       — 开关播报")
		fmt.Println("    /voice test         — 用当前声线播一句话")
		fmt.Println("    /voice voices       — 列出可用中文女声")
		fmt.Println("    /voice 声线 <名称>   — 切换声线 (如 zh-CN-XiaoyiNeural)")
		fmt.Println(dim("  默认声线: zh-CN-XiaoxiaoNeural (晓晓)。声音由微软 edge-tts 免费合成。"))
		fmt.Println()
		return
	}

	sub := strings.ToLower(parts[1])
	switch sub {
	case "on", "off":
		voiceMu.Lock()
		voiceEnabled = (sub == "on")
		st := "开"
		if !voiceEnabled {
			st = "关"
		}
		voiceMu.Unlock()
		fmt.Printf("  %s 语音播报已%s\n", color(ansi.green, "✓"), st)
	case "test":
		if err := voiceSpeak("你好，我是铸剑炉，很高兴用声音陪伴你。"); err != nil {
			fmt.Printf("  %s 测试失败: %v\n", color(ansi.red, "✗"), err)
		} else {
			fmt.Printf("  %s 测试播报已发声\n", color(ansi.green, "✓"))
		}
	case "voices":
		fmt.Println("  可用中文女声 (声线/名称):")
		for _, v := range knownVoices {
			fmt.Printf("    %s\n", dim(v))
		}
		fmt.Println(dim("  用 /voice 声线 <名称> 切换。"))
	default:
		// 尝试当作声线切换: 支持 "/voice zh-CN-XiaoyiNeural" 或 "/voice 声线 zh-CN-XiaoyiNeural"
		raw := strings.Join(parts[1:], " ")
		raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(raw), "声线"), "voice"))
		if isValidVoice(raw) {
			voiceMu.Lock()
			voiceName = raw
			voiceMu.Unlock()
			fmt.Printf("  %s 已切换声线: %s\n", color(ansi.green, "✓"), raw)
			_ = voiceSpeak("好的，声线已切换。")
		} else {
			fmt.Printf("  %s 未知声线: %s\n", color(ansi.yellow, "⚠"), raw)
			fmt.Println(dim("  输入 /voice voices 查看可用声线。"))
		}
	}
	fmt.Println()
}

func isValidVoice(v string) bool {
	for _, k := range knownVoices {
		if strings.EqualFold(k, v) {
			return true
		}
	}
	return false
}
