# Cross Platform Build

Wails desktop packages should be built on each target OS.

## Windows

```powershell
cd desktop-client
wails build
```

Output:

```text
desktop-client/build/bin/cloudflare-tunnel-desk.exe
```

## macOS

Run on macOS:

```bash
cd desktop-client
wails build -platform darwin/universal
```

## Linux

Run on Linux with Wails dependencies installed:

```bash
cd desktop-client
wails build -platform linux/amd64
```

## GitHub Actions Recommendation

Use separate `windows-latest`, `macos-latest`, and `ubuntu-latest` runners, then upload the generated binaries as release assets.
