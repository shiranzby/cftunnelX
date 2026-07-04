# cftunnelX Desktop Client

This is a Wails v2 desktop client for the v4 WebUI.

## Current Status

- It reuses the existing WebUI instead of duplicating dashboard logic.
- It keeps CLI and GUI modes side by side.

## Requirements

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails doctor
```

Windows also requires the WebView2 runtime. macOS and Linux require the normal Wails platform dependencies.

## Development

```powershell
wails dev
```

## Build

```powershell
wails build
```

Cross-platform release builds should be done with GitHub Actions runners for Windows, macOS, and Linux.
