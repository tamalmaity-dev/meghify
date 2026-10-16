# Measures the panel's steady-state footprint, which is the project's headline claim.
$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
$Exe = Join-Path $Root "bin\meghify.exe"
$Data = Join-Path $Root ".devdata"

Get-Process -Name meghify -ErrorAction SilentlyContinue | Stop-Process -Force
Remove-Item -Recurse -Force $Data -ErrorAction SilentlyContinue

$env:DATA_DIR = $Data
$env:AUTH_SECRET = "dev-secret-at-least-16-chars"
$env:PANEL_ADDR = "127.0.0.1:8080"
$env:DOCKER_HOST = "tcp://127.0.0.1:2375"
$env:PHP_MEMORY_LIMIT = ""

Start-Process -FilePath $Exe `
  -RedirectStandardOutput (Join-Path $Root "dev-run.log") `
  -RedirectStandardError (Join-Path $Root "dev-run.err.log") -WindowStyle Hidden
Start-Sleep -Seconds 4

function Show-WhereWeAre($label) {
  $proc = Get-Process -Name meghify | Select-Object -First 1
  $ws = [math]::Round(($proc.WorkingSet64 / 1MB), 1)
  $pws = [math]::Round(($proc.PrivateMemorySize64 / 1MB), 1)
  Write-Output ("{0,-44} {1,8} MB working set  /  {2,8} MB private" -f $label, $ws, $pws)
}

Show-WhereWeAre "idle (panel + embedded SPA + sqlite)"

& node (Join-Path $Root "scripts\load-test.mjs") 2>&1 | Select-Object -First 10
Start-Sleep -Seconds 2
Show-WhereWeAre "after 200 requests + 25 applications"

$proc = Get-Process -Name meghify | Select-Object -First 1
$cpuStart = $proc.CPU
Start-Sleep -Seconds 5
$cpuEnd = (Get-Process -Name meghify | Select-Object -First 1).CPU
Write-Output ("{0,-44} {1,8} s of cpu in a 5 s idle window" -f "idle cpu", [math]::Round($cpuEnd - $cpuStart, 2))

$size = [math]::Round(((Get-Item $Exe).Length / 1MB), 1)
Write-Output ("{0,-44} {1,8} MB on disk" -f "embedded binary size", $size)

Get-Process -Name meghify -ErrorAction SilentlyContinue | Stop-Process -Force
