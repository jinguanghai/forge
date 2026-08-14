# ============================================================
#  铸剑炉一键升级 v1.0  (upgrade.ps1)
#  流程: ①自动验证 → ②防御预检 → ③主人确认 → ④关旧进程/就位新版
#        → ⑤启动新版 → ⑥存活检查(失败自动回滚)
#  用法: 双击 upgrade.cmd 即可；技术自检: upgrade.cmd -DryRun
# ============================================================
param([switch]$DryRun)

$ErrorActionPreference = 'Continue'
$AppDir   = $PSScriptRoot
$APP     = 'forge.exe'
$NEW     = 'forge_new.exe'
$GUARD   = 'defense_system\forge_guard.py'
Set-Location $AppDir

function C($m)  { Write-Host $m -ForegroundColor Cyan }
function OK($m) { Write-Host "  [OK]  $m" -ForegroundColor Green }
function NG($m) { Write-Host "  [!!]  $m" -ForegroundColor Red }
function WN($m) { Write-Host "  [!]   $m" -ForegroundColor Yellow }

C "=============================================="
C "   铸剑炉一键升级 (自动验证 + 主人确认)"
C "=============================================="
if ($DryRun) { WN "演练模式 -DryRun: 只验证不替换, 不碰任何进程" }

# ---------- [1/6] 自动验证 ----------
C "`n[1/6] 自动验证: go vet / build / test"
$fail = $false
C "  -- go vet --"
& go vet ./... 2>&1 | Out-Host
if ($LASTEXITCODE -ne 0) { NG "go vet 失败"; $fail = $true } else { OK "go vet 通过" }

if (-not $fail) {
  C "  -- go build (重新编译 $NEW, 保证 exe 与源码一致) --"
  & go build -o $NEW . 2>&1 | Out-Host
  if ($LASTEXITCODE -ne 0) { NG "go build 失败"; $fail = $true } else { OK "编译成功: $NEW" }
}
if (-not $fail) {
  C "  -- go test --"
  & go test ./... 2>&1 | Out-Host
  if ($LASTEXITCODE -ne 0) { NG "go test 失败"; $fail = $true } else { OK "全部测试通过" }
}
if ($fail) {
  NG "自动验证未通过, 已中止。未改动任何文件/进程。"
  Read-Host "`n按回车退出"
  exit 1
}
OK "自动验证全部通过"

# ---------- [2/6] 防御预检 ----------
C "`n[2/6] 防御完整性预检 (forge_guard check)"
& python $GUARD check 2>&1 | Out-Host
if ($LASTEXITCODE -eq 0) { OK "防御基线完好" }
else { WN "基线有差异(通常是源码/exe 预期变更), 在确认环节请复核" }

# ---------- [3/6] 版本信息 + 主人确认 ----------
$old = Get-Item $APP -ErrorAction SilentlyContinue
$new = Get-Item $NEW -ErrorAction SilentlyContinue
if (-not $new) { NG "$NEW 不存在(编译失败?), 中止"; Read-Host "按回车退出"; exit 1 }
if (-not $old) { NG "$APP 不存在, 中止"; Read-Host "按回车退出"; exit 1 }
C "`n[3/6] 待替换版本:"
C ("      当前: {0}  ({1:N0} B, {2})" -f $APP, $old.Length, $old.LastWriteTime.ToString("yyyy-MM-dd HH:mm:ss"))
C ("      新版: {0}  ({1:N0} B, {2})" -f $NEW, $new.Length, $new.LastWriteTime.ToString("yyyy-MM-dd HH:mm:ss"))

$running = Get-Process -Name 'forge' -ErrorAction SilentlyContinue
if ($running) {
  WN ("注意: 当前有 forge.exe 正在运行 (PID {0}), 确认后将被关闭" -f ($running.Id -join ', '))
}

if ($DryRun) {
  OK "演练模式: 验证全部通过, 跳过替换(不关进程/不换 exe)"
  Read-Host "`n按回车退出"
  exit 0
}

$ans = Read-Host "`n确认替换并重启? 输入 Y 确认 / N 取消"
if ($ans -notmatch '^[Yy]$') { WN "已取消, 未做任何改动。"; exit 0 }

# ---------- [4/6] 关旧进程 + 备份 + 就位 + 基线同步 ----------
C "`n[4/6] 关闭旧进程..."
& taskkill /F /IM forge.exe 2>&1 | Out-Host
Start-Sleep -Seconds 2

$stamp = Get-Date -Format "yyyyMMdd_HHmmss"
$bak = "$APP.bak_$stamp"
C "  备份旧版 -> $bak"
Move-Item -Force $APP $bak
OK "  旧版已备份"

C "  就位新版 -> $APP"
Move-Item -Force $NEW $APP
OK "  新版已就位"

C "  轮转备份(只留最近10个)..."
Get-ChildItem -Filter "forge.exe.bak_*" | Sort-Object LastWriteTime -Descending |
  Select-Object -Skip 10 | Remove-Item -Force -ErrorAction SilentlyContinue
OK "  备份轮转完成"

C "  同步防御基线..."
& python $GUARD init 2>&1 | Out-Host
OK "  基线已同步"

# ---------- [5/6] 启动新版 + 存活检查 ----------
C "`n[5/6] 启动新版..."
Start-Process -FilePath "$AppDir\$APP" -WorkingDirectory $AppDir
Start-Sleep -Seconds 5
$alive = Get-Process -Name 'forge' -ErrorAction SilentlyContinue
if ($alive) {
  OK ("新版已启动并存活 (PID {0})" -f ($alive.Id -join ', '))
} else {
  NG "新版未存活, 自动回滚..."
  $latest = Get-ChildItem -Filter "forge.exe.bak_*" | Sort-Object LastWriteTime -Descending |
            Select-Object -First 1
  if ($latest) {
    & taskkill /F /IM forge.exe 2>&1 | Out-Null
    Copy-Item -Force $latest.FullName $APP
    Start-Process -FilePath "$AppDir\$APP" -WorkingDirectory $AppDir
    Start-Sleep -Seconds 5
    if (Get-Process -Name 'forge' -ErrorAction SilentlyContinue) {
      OK ("已回滚到 {0} 并重新启动" -f $latest.Name)
    } else {
      NG "回滚后仍无法启动! 请手动检查。备份仍在: $($latest.FullName)"
    }
  } else { NG "无备份可回滚!" }
}

C "`n[6/6] 升级流程结束。新窗口应显示欢迎画面, 输入 /cache 可验证功能。"
Read-Host "`n按回车退出"
