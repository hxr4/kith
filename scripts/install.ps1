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

New-Item -ItemType Directory -Force -Path $Dir | Out-Null
$svc = Get-Service -Name kith -ErrorAction SilentlyContinue
$wasRunning = $svc -and $svc.Status -eq "Running"
if ($wasRunning) {
    Write-Host "Stopping the kith service to replace its binary (needs an elevated terminal)..."
    Stop-Service kith
}
if ($Binary) {
    Copy-Item -Force $Binary $Exe
} else {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw "Go is required to build kith (winget install GoLang.Go), or pass -Binary <kith.exe>" }
    Push-Location (Join-Path $PSScriptRoot "..")
    $env:CGO_ENABLED = "0"
    go build -trimpath -o $Exe ./cmd/kith
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
