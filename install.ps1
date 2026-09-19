# cftunnelX Windows 安装脚本
# 用法: irm https://raw.githubusercontent.com/shiranzby/cftunnelX/main/install.ps1 | iex
$ErrorActionPreference = "Stop"
$repo = "shiranzby/cftunnelX"
$installDir = "$env:LOCALAPPDATA\cftunnelX"

$arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { "amd64" }
$url = "https://github.com/$repo/releases/latest/download/cftunnelX_windows_$arch.zip"

Write-Host "正在下载 cftunnelX (windows/$arch)..."
$tmp = New-TemporaryFile | Rename-Item -NewName { $_.Name + ".zip" } -PassThru
Invoke-WebRequest -Uri $url -OutFile $tmp.FullName

New-Item -ItemType Directory -Force -Path $installDir | Out-Null
Expand-Archive -Path $tmp.FullName -DestinationPath $installDir -Force
Remove-Item $tmp.FullName

# 添加到用户 PATH（持久化 + 当前会话立即生效）
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ([string]::IsNullOrEmpty($userPath)) { $userPath = "" }
if ($userPath -notlike "*$installDir*") {
    $newPath = if ($userPath.TrimEnd(';')) { "$userPath;$installDir" } else { $installDir }
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    $env:Path += ";$installDir"
    Write-Host "已添加 $installDir 到 PATH（当前会话立即生效）"
}

# 安装自检
$exe = Join-Path $installDir "cftunnelX.exe"
if (Test-Path $exe) {
    try {
        $ver = & $exe version 2>&1 | Select-Object -First 1
        Write-Host "cftunnelX 已安装到 $exe ($ver)"
    } catch {
        Write-Host "警告: cftunnelX 已复制到 $exe，但自检未通过"
    }
} else {
    Write-Host "警告: 未在 $installDir 中找到 cftunnelX.exe，请检查压缩包内容"
}

Write-Host "运行 cftunnelX quick <端口> 开始使用"
