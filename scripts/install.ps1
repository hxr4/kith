# Install (or update / remove) the kith CLI for the current user on Windows.
#   powershell -ExecutionPolicy Bypass -File scripts\install.ps1             build from source (or use -Binary)
#   powershell -ExecutionPolicy Bypass -File scripts\install.ps1 -Binary C:\path\kith.exe
#   powershell -ExecutionPolicy Bypass -File scripts\install.ps1 -Uninstall
param([string]$Binary = "", [switch]$Uninstall)
$ErrorActionPreference = "Stop"

$Dir = Join-Path $env:LOCALAPPDATA "Programs\kith"
$Exe = Join-Path $Dir "kith.exe"
$Begin = "# >>> kith >>>"
$End = "# <<< kith <<<"

function Remove-ProfileBlock {
    if (Test-Path $PROFILE) {
        $c = Get-Content $PROFILE -Raw
        $c = [regex]::Replace($c, "(?s)\r?\n?$([regex]::Escape($Begin)).*?$([regex]::Escape($End))\r?\n?", "")
        Set-Content -Path $PROFILE -Value $c -NoNewline
    }
}

if ($Uninstall) {
    Remove-Item -Force -ErrorAction SilentlyContinue $Exe
    $p = [Environment]::GetEnvironmentVariable("Path", "User")
    $p = ($p -split ";" | Where-Object { $_ -and ($_ -ne $Dir) }) -join ";"
    [Environment]::SetEnvironmentVariable("Path", $p, "User")
    Remove-ProfileBlock
    Write-Host "Removed kith from $Dir (identity, peers and tasks in ~\.kith are kept)."
    Write-Host "If the service is installed, remove it first in an elevated terminal: kith daemon uninstall"
    exit 0
}

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator)
$svc = Get-Service -Name kith -ErrorAction SilentlyContinue
$wasRunning = $svc -and $svc.Status -eq "Running"
# Check elevation up front: replacing the running service's binary means
# stopping the service, which needs Administrator rights.
if ($wasRunning -and -not $isAdmin) {
    Write-Host "The kith service is running and must be stopped to replace its binary." -ForegroundColor Yellow
    Write-Host "Re-run this script from PowerShell opened with 'Run as administrator'." -ForegroundColor Yellow
    exit 1
}

New-Item -ItemType Directory -Force -Path $Dir | Out-Null
if ($wasRunning) {
    Write-Host "Stopping the kith service to replace its binary..."
    Stop-Service kith
}
if ($Binary) {
    Copy-Item -Force $Binary $Exe
} else {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw "Go is required to build kith (winget install GoLang.Go), or pass -Binary <kith.exe>" }
    Push-Location (Join-Path $PSScriptRoot "..")
    $env:CGO_ENABLED = "0"
    Write-Host "Building kith from source (a first build downloads modules and can take a few minutes)..."
    $sw = [Diagnostics.Stopwatch]::StartNew()
    go build -trimpath -o $Exe ./cmd/kith
    if ($LASTEXITCODE -ne 0) { Pop-Location; if ($wasRunning) { Start-Service kith }; throw "go build failed" }
    Write-Host ("Built in {0:N0}s." -f $sw.Elapsed.TotalSeconds)
    Pop-Location
}
if ($wasRunning) { Start-Service kith }

$p = [Environment]::GetEnvironmentVariable("Path", "User")
if (-not (($p -split ";") -contains $Dir)) {
    [Environment]::SetEnvironmentVariable("Path", ($p.TrimEnd(";") + ";" + $Dir), "User")
    Write-Host "Added $Dir to your user PATH (open a new terminal)."
}
$env:Path = "$Dir;$env:Path"

# PowerShell tab completion.
if (-not (Test-Path $PROFILE)) { New-Item -ItemType File -Force -Path $PROFILE | Out-Null }
if (-not (Select-String -Path $PROFILE -SimpleMatch $Begin -Quiet)) {
    Add-Content -Path $PROFILE -Value "`n$Begin`nif (Get-Command kith -ErrorAction SilentlyContinue) { kith completion powershell | Out-String | Invoke-Expression }`n$End"
    Write-Host "Added kith tab completion to $PROFILE."
}
Write-Host ("Installed " + (& $Exe --version) + " -> $Exe")
Write-Host "Try: kith status"
