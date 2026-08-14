# open_source_sync.ps1 — Sync local Chinese repo -> open-source English repo
# Usage: powershell -ExecutionPolicy Bypass -File D:\forge\open_source_sync.ps1
# Copies tracked files from the local working repo to the open-source repo,
# commits and pushes. Comments/docs are expected to stay in English there;
# runtime strings remain Chinese by design (product feature).

$ErrorActionPreference = "Stop"
$src = "D:\forge"
$dst = "D:\forge_open_source"
$tokenFile = "$src\.forge-temp\gh_token.txt"

Write-Host "[1/4] Copying tracked files from $src ..."
# Get tracked files from the local repo (excludes .env, memory.json, knowledge, etc.)
$tracked = & git -C $src ls-files
$count = 0
foreach ($rel in $tracked) {
    $from = Join-Path $src $rel
    $to   = Join-Path $dst $rel
    if (-not (Test-Path $from)) { continue }
    $toDir = Split-Path $to -Parent
    if (-not (Test-Path $toDir)) { New-Item -ItemType Directory -Path $toDir -Force | Out-Null }
    Copy-Item $from $to -Force
    $count++
}
Write-Host "  Copied $count files."

# Delete files in dst that are no longer tracked in src (cleanup)
$dstTracked = & git -C $dst ls-files
foreach ($rel in $dstTracked) {
    if ($tracked -notcontains $rel) {
        $to = Join-Path $dst $rel
        if (Test-Path $to) { Remove-Item $to -Force; Write-Host "  Removed $rel" }
    }
}

Write-Host "[2/4] Committing in $dst ..."
& git -C $dst add -A
$ts = Get-Date -Format "yyyyMMdd_HHmmss"
& git -C $dst commit -m "sync from local repo @ $ts"

Write-Host "[3/4] Pushing to GitHub ..."
if (-not (Test-Path $tokenFile)) { throw "token file not found: $tokenFile" }
$token = (Get-Content $tokenFile -Raw).Trim()
$askpass = Join-Path $env:TEMP "gh_askpass.bat"
Set-Content -Path $askpass -Value "@echo off`necho $token" -Encoding ASCII
$env:GIT_ASKPASS = $askpass
$env:GIT_TERMINAL_PROMPT = "0"
& git -C $dst push origin main --force
Remove-Item $askpass -Force -ErrorAction SilentlyContinue

Write-Host "[4/4] Done. Remote HEAD:"
& git -C $dst ls-remote origin HEAD
Write-Host "Sync complete."
