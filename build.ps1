# 一键交叉编译脚本（Windows PowerShell）
# 产物输出到 dist/ 目录，使用 -ldflags "-s -w" 减小体积。
#
# 用法示例（-p 是 -Platform 的简写）：
#   ./build.ps1                # 编译全部平台（默认）
#   ./build.ps1 -p windows     # 只编译 Windows exe
#   ./build.ps1 -p mac         # 只编译 macOS（amd64 + arm64）
#   ./build.ps1 -p linux       # 只编译 Linux
#   ./build.ps1 -Version v1.2.0  # 手动指定版本号（不指定则自动取 git tag）
#   ./build.ps1 windows v1.2.0   # 位置参数：平台 + 版本号

param(
    # 目标平台：all（默认）/ windows / linux / mac / darwin。简写：-p
    [Alias("p")]
    [string]$Platform = "all",
    # 版本号：不指定则自动从 git describe 获取
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"

$AppName = "aceshare"
$DistDir = "dist"

# 版本信息：优先用 -Version 参数，其次用 git 描述，最后回退到 v0.0.0。
if (-not $Version) {
    $v = (git describe --tags --always --dirty 2>$null)
    $Version = if ($LASTEXITCODE -eq 0 -and $v) { $v.Trim() } else { "v0.0.0" }
}
$Commit = (git rev-parse --short HEAD 2>$null)
if ($LASTEXITCODE -ne 0 -or -not $Commit) { $Commit = "unknown" } else { $Commit = $Commit.Trim() }
$BuildTime = (Get-Date -Format "yyyy-MM-dd")

Write-Host "平台：$Platform  版本：$Version  提交：$Commit  构建时间：$BuildTime" -ForegroundColor Cyan

$LdFlags = "-s -w -X main.version=$Version -X main.commit=$Commit -X main.buildTime=$BuildTime"

# 全部可选目标平台：GOOS/GOARCH/输出文件名
$AllTargets = @(
    @{ OS = "windows"; Arch = "amd64"; Out = "$AppName-windows-amd64.exe" },
    @{ OS = "linux";   Arch = "amd64"; Out = "$AppName-linux-amd64" },
    @{ OS = "darwin";  Arch = "amd64"; Out = "$AppName-macos-amd64" },
    @{ OS = "darwin";  Arch = "arm64"; Out = "$AppName-macos-arm64" }
)

# 根据 -Platform 参数筛选要编译的目标。
switch ($Platform.ToLower()) {
    "all"     { $Targets = $AllTargets }
    "windows" { $Targets = $AllTargets | Where-Object { $_.OS -eq "windows" } }
    "win"     { $Targets = $AllTargets | Where-Object { $_.OS -eq "windows" } }
    "linux"   { $Targets = $AllTargets | Where-Object { $_.OS -eq "linux" } }
    "mac"     { $Targets = $AllTargets | Where-Object { $_.OS -eq "darwin" } }
    "macos"   { $Targets = $AllTargets | Where-Object { $_.OS -eq "darwin" } }
    "darwin"  { $Targets = $AllTargets | Where-Object { $_.OS -eq "darwin" } }
    default {
        Write-Error "未知平台：$Platform（可选：all / windows / linux / mac）"
        exit 1
    }
}

if (Test-Path $DistDir) {
    Remove-Item -Recurse -Force $DistDir
}
New-Item -ItemType Directory -Path $DistDir | Out-Null

# 生成 Windows exe 图标资源（.syso）。Go 编译 Windows 目标时会自动链接它。
# 使用一次性工具 rsrc，不会加入项目依赖；非 Windows 目标会自动忽略该文件。
if ((Test-Path "logo.ico") -and -not (Test-Path "rsrc_windows.syso")) {
    Write-Host "生成 exe 图标资源 rsrc_windows.syso"
    go run github.com/akavel/rsrc@latest -ico logo.ico -o rsrc_windows.syso
    if ($LASTEXITCODE -ne 0) {
        Write-Warning "生成图标资源失败，将编译无图标版本"
    }
}

# 禁用 CGO，保证静态、零依赖单文件。
$env:CGO_ENABLED = "0"

foreach ($t in $Targets) {
    $env:GOOS = $t.OS
    $env:GOARCH = $t.Arch
    $outPath = Join-Path $DistDir $t.Out
    Write-Host "编译 $($t.OS)/$($t.Arch) -> $outPath"
    go build -trimpath -ldflags $LdFlags -o $outPath .
    if ($LASTEXITCODE -ne 0) {
        Write-Error "编译 $($t.OS)/$($t.Arch) 失败"
        exit 1
    }
}

Write-Host ""
Write-Host "全部完成，产物位于 $DistDir/ ：" -ForegroundColor Green
Get-ChildItem $DistDir | Format-Table Name, Length
