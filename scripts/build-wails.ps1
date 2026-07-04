$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..\desktop-client")
wails build
