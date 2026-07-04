# cftunnelX Windows 安装脚本
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

# 添加到用�?PATH（持久化 + 当前会话立即生效�?$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$installDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$installDir", "User")
    $env:Path += ";$installDir"
    Write-Host "已添�?$installDir �?PATH（当前会话立即生效）"
}

Write-Host "cftunnelX 已安装到 $installDir\cftunnelX.exe"
Write-Host "运行 cftunnelX quick <端口> 开始使�?
