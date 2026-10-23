package main

import (
	"os"
	"strings"
	"testing"
)

// linux_systemd_sentinel_test.go —— systemd 单元判据 (T22 第 2 层)。
//
// 为什么需要: 计划任务在 Linux 上不存在, 巡检体系(hourly 11 项 / 缓存轮转 / 代理自愈)
// 会整体静默失效 —— 而「静默失效」的典型症状是几个月后才发现「守卫早就不跑了」。
// 单元文件是死程序, 但它们的契约(WorkingDirectory / Persistent 对齐 / ExecStart 指向
// 真实脚本 / install.sh 的 LF)没有任何东西钉住。
//
// 本机无 Linux, 无法 systemd-analyze verify; 可判定的结构事实(键存在、指向存在、
// 语义位对齐)全部在这里钉住, 运行期由真机验证(见 TODO T22 边界)。

const systemdDir = "defense_system/systemd"

var systemdUnits = []string{
	"forge-guard.service", "forge-guard.timer",
	"forge-cache.service", "forge-cache.timer",
	"forge-proxy.service", "forge-proxy.timer",
}

func readSystemdFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(systemdDir + "/" + name)
	if err != nil {
		t.Errorf("读 %s: %v", name, err)
		return ""
	}
	return string(b)
}

func TestSystemdUnitsExist(t *testing.T) {
	for _, name := range systemdUnits {
		if s := readSystemdFile(t, name); s != "" && !strings.Contains(s, "[Unit]") {
			t.Errorf("%s 缺 [Unit] 段", name)
		}
	}
	if _, err := os.Stat(systemdDir + "/install.sh"); err != nil {
		t.Errorf("缺 install.sh: %v", err)
	}
}

// TestSystemdServicesHaveWorkingDirectory: 巡检脚本按相对路径找仓库根,
// 从别处启动会扫错根(与 Windows 侧计划任务的 WorkingDirectory 同义, 是必需项)。
func TestSystemdServicesHaveWorkingDirectory(t *testing.T) {
	for _, name := range systemdUnits {
		if !strings.HasSuffix(name, ".service") {
			continue
		}
		s := readSystemdFile(t, name)
		if !strings.Contains(s, "WorkingDirectory=@@WORKDIR@@") {
			t.Errorf("%s 缺 WorkingDirectory=@@WORKDIR@@", name)
		}
		if !strings.Contains(s, "Type=oneshot") {
			t.Errorf("%s 必须是 Type=oneshot (巡检是一次性任务)", name)
		}
	}
}

// TestSystemdExecStartTargetsExist: ExecStart 指向的脚本必须真实存在。
// 脚本改名/搬家后单元会静默失效 —— systemd 只会在触发时报 "No such file",
// 而没人天天看 journal。
func TestSystemdExecStartTargetsExist(t *testing.T) {
	for _, name := range systemdUnits {
		if !strings.HasSuffix(name, ".service") {
			continue
		}
		s := readSystemdFile(t, name)
		found := false
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "ExecStart=") {
				continue
			}
			found = true
			fields := strings.Fields(strings.TrimPrefix(line, "ExecStart="))
			if len(fields) < 2 {
				t.Errorf("%s 的 ExecStart 形如 %q, 缺脚本参数", name, line)
				continue
			}
			script := strings.TrimPrefix(fields[len(fields)-1], "@@WORKDIR@@/")
			if _, err := os.Stat(script); err != nil {
				t.Errorf("%s 的 ExecStart 指向不存在的脚本 %s", name, script)
			}
		}
		if !found {
			t.Errorf("%s 缺 ExecStart", name)
		}
	}
}

// TestSystemdTimerPersistentMatchesWindows: Persistent 必须与 Windows 侧
// StartWhenAvailable 对齐 —— 错过的时间点该补跑的补跑(guard/cache), 幂等守护不补(proxy)。
func TestSystemdTimerPersistentMatchesWindows(t *testing.T) {
	want := map[string]bool{
		"forge-guard.timer": true,  // ForgeGuardHourly: StartWhenAvailable=true
		"forge-cache.timer": true,  // CacheGuardDaily: StartWhenAvailable=true
		"forge-proxy.timer": false, // ForgeProxyAuto: 无该位
	}
	for name, wantPersistent := range want {
		s := readSystemdFile(t, name)
		has := strings.Contains(s, "Persistent=true")
		if has != wantPersistent {
			t.Errorf("%s Persistent=%v, 期望 %v (须与 Windows 侧 StartWhenAvailable 对齐)", name, has, wantPersistent)
		}
		if !strings.Contains(s, "WantedBy=timers.target") {
			t.Errorf("%s 缺 [Install] WantedBy=timers.target (否则 enable 无效)", name)
		}
	}
}

// TestSystemdInstallShContract: 安装脚本必须 LF、必须有 root 检查与 daemon-reload。
// 缺 daemon-reload = 改了单元不生效; 缺 root 检查 = 权限不足时报一堆无关错误。
func TestSystemdInstallShContract(t *testing.T) {
	s := readSystemdFile(t, "install.sh")
	if strings.Contains(s, "\r\n") {
		t.Errorf("install.sh 含 CRLF —— sh 会执行失败")
	}
	for _, want := range []string{"systemctl daemon-reload", "systemctl enable --now", "id -u", "command -v python3", "@@WORKDIR@@"} {
		if !strings.Contains(s, want) {
			t.Errorf("install.sh 缺 %q", want)
		}
	}
}
