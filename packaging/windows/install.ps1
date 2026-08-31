# StealthScale Windows installer — native named-pipe service without Docker.
# Usage:
#   powershell -ExecutionPolicy Bypass -File install.ps1 [-BinaryPath .\stscale.exe] [-ConfigPath .\config.yaml] [-LaunchAtStartup]
#
# This script:
#   - Copies stscale.exe to $env:ProgramFiles\stealthscale\
#   - Installs config to $env:ProgramData\stealthscale\config.yaml (if not exists)
#   - Creates a Windows service via sc.exe (fallback) or New-Service
#   - Optionally creates a Run registry key for --tray autostart (when tray is implemented)
#   - Does NOT require Docker.

param(
    [string]$BinaryPath = ".\stscale.exe",
    [string]$ConfigPath = ".\config.yaml",
    [switch]$LaunchAtStartup
)

$ErrorActionPreference = "Stop"

$programFiles = $env:ProgramFiles
if (-not $programFiles) { $programFiles = "C:\Program Files" }
$programData = $env:ProgramData
if (-not $programData) { $programData = "C:\ProgramData" }

$installDir = Join-Path $programFiles "stealthscale"
$configDir = Join-Path $programData "stealthscale"
$binDst = Join-Path $installDir "stscale.exe"
$configDst = Join-Path $configDir "config.yaml"
$serviceName = "StealthScale"

Write-Host "[*] Installing StealthScale to $installDir"

if (-not (Test-Path $installDir)) {
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
}
if (-not (Test-Path $configDir)) {
    New-Item -ItemType Directory -Path $configDir -Force | Out-Null
}

if (-not (Test-Path $BinaryPath)) {
    Write-Error "Binary not found at $BinaryPath. Build with: GOOS=windows go build -o stscale.exe ./cmd/stealthscale"
    exit 1
}

Write-Host "[*] Copying binary to $binDst"
Copy-Item -Path $BinaryPath -Destination $binDst -Force

if (Test-Path $configDst) {
    Write-Host "[*] $configDst already exists, not overwriting"
} else {
    if (Test-Path $ConfigPath) {
        Write-Host "[*] Installing config to $configDst"
        Copy-Item -Path $ConfigPath -Destination $configDst
    } else {
        Write-Host "[*] No config at $ConfigPath — creating example at $configDst"
        # Try to find config-example.yaml next to script
        $example = Join-Path $PSScriptRoot "..\..\config-example.yaml"
        if (Test-Path $example) {
            Copy-Item -Path $example -Destination $configDst
        } else {
            Write-Host "[!] Edit $configDst with your server_url and noise.private_key_path"
            New-Item -ItemType File -Path $configDst -Force | Out-Null
        }
    }
}

# Service creation
$binWithArgs = "`"$binDst`" serve --config `"$configDst`""

Write-Host "[*] Checking for existing service $serviceName"
$existing = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
if ($existing) {
    Write-Host "[*] Service already exists, updating binary path"
    # Stop before updating
    try { Stop-Service -Name $serviceName -Force -ErrorAction SilentlyContinue } catch {}
    # Use sc.exe to update binPath
    & sc.exe config $serviceName binPath= $binWithArgs | Out-Null
} else {
    Write-Host "[*] Creating Windows service $serviceName"
    # Prefer sc.exe (works on all Windows, handles spaces)
    $scArgs = "create $serviceName binPath= `"$binWithArgs`" start= auto DisplayName= `"StealthScale Control Server`""
    # Use New-Service if available, fallback to sc.exe
    try {
        New-Service -Name $serviceName -BinaryPathName $binWithArgs -DisplayName "StealthScale Control Server" -Description "StealthScale — VLESS+Reality control plane" -StartupType Automatic -ErrorAction Stop | Out-Null
    } catch {
        Write-Host "[*] New-Service failed, falling back to sc.exe"
        & sc.exe create $serviceName binPath= $binWithArgs start= auto DisplayName= "StealthScale Control Server" | Out-Null
        if ($LASTEXITCODE -ne 0) {
            Write-Error "Failed to create service via sc.exe"
            exit 1
        }
    }
}

# Recovery: restart on failure
& sc.exe failure $serviceName reset= 86400 actions= restart/5000/restart/5000/restart/5000 | Out-Null

Write-Host "[*] Starting service"
try { Start-Service -Name $serviceName -ErrorAction Stop } catch {
    Write-Warning "Start-Service failed: $_"
    Write-Host "[*] Try manually: sc.exe start $serviceName"
}

if ($LaunchAtStartup) {
    $runKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
    $runValue = "`"$binDst`" serve --tray --config `"$configDst`""
    Write-Host "[*] Enabling autostart at $runKey"
    New-Item -Path $runKey -Force | Out-Null
    Set-ItemProperty -Path $runKey -Name "StealthScale" -Value $runValue
    Write-Host "[*] Autostart enabled (tray mode requires tray binary, see windows-hide-in-tray)"
}

Write-Host "[*] Done."
Write-Host "[*] Service: sc.exe query $serviceName"
Write-Host "[*] Logs: event viewer or $configDir\logs"
Write-Host "[*] CLI: `"$binDst`" --address npipe:////./pipe/stealthscale nodes list"
Write-Host "[*] Uninstall: sc.exe stop $serviceName; sc.exe delete $serviceName; Remove-Item -Recurse $installDir,$configDir"
