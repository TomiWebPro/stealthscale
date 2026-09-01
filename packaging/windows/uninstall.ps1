# StealthScale Windows uninstaller — clean removal.
# Usage:
#   powershell -ExecutionPolicy Bypass -File uninstall.ps1 [-Purge]
#
# - Without -Purge: removes service, binary, autostart key, keeps %ProgramData%\stealthscale (config/db).
# - With -Purge: also deletes %ProgramData%\stealthscale (config, db.sqlite, .xray_secret) and logs.
#
# Also available via: stscale uninstall [--purge]  (same logic, cross-platform)

param(
    [switch]$Purge
)

$ErrorActionPreference = "Continue"

$programFiles = $env:ProgramFiles
if (-not $programFiles) { $programFiles = "C:\Program Files" }
$programData = $env:ProgramData
if (-not $programData) { $programData = "C:\ProgramData" }

$installDir = Join-Path $programFiles "stealthscale"
$configDir = Join-Path $programData "stealthscale"
$serviceName = "StealthScale"

Write-Host "[*] Uninstalling StealthScale (Windows) Purge=$Purge"

# Stop service
Write-Host "[*] Stopping service $serviceName (if present)"
try { Stop-Service -Name $serviceName -Force -ErrorAction SilentlyContinue } catch {}
& sc.exe stop $serviceName 2>$null | Out-Null
Start-Sleep -Seconds 2

# Delete service
Write-Host "[*] Deleting service $serviceName"
& sc.exe delete $serviceName 2>$null | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Host "[*] sc.exe delete exit=$LASTEXITCODE (may already be gone)"
}

# Remove Run key (tray autostart)
$runKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
Write-Host "[*] Removing autostart key $runKey\StealthScale"
try { Remove-ItemProperty -Path $runKey -Name "StealthScale" -Force -ErrorAction SilentlyContinue } catch {}
& reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Run" /v StealthScale /f 2>$null | Out-Null

# Remove binary dir
Write-Host "[*] Removing $installDir"
if (Test-Path $installDir) {
    try { Remove-Item -Recurse -Force $installDir } catch { Write-Warning "Failed to remove $installDir : $_" }
} else {
    Write-Host "[*] $installDir already gone"
}

if ($Purge) {
    Write-Host "[*] Purging $configDir (config, db, .xray_secret)"
    if (Test-Path $configDir) {
        try { Remove-Item -Recurse -Force $configDir } catch { Write-Warning "Failed to remove $configDir : $_" }
    }
    Write-Host "[*] Purge complete"
} else {
    Write-Host "[*] Kept $configDir (use -Purge to delete config & db)"
}

Write-Host "[*] Done. Named pipe \\.\pipe\stealthscale is ephemeral (no file to delete)."
Write-Host "[*] Alternative: stscale uninstall --purge  (cross-platform CLI)"
