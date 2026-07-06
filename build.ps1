# 一键交叉编译脚本（Windows PowerShell）
# 产物输出到 dist/ 目录，使用 -ldflags "-s -w" 减小体积。

$ErrorActionPreference = "Stop"

$AppName = "aceshare"
$DistDir = "dist"
$LdFlags = "-s -w"

# 目标平台：GOOS/GOARCH/输出文件名
$Targets = @(
    @{ OS = "windows"; Arch = "amd64"; Out = "$AppName-windows-amd64.exe" },
    @{ OS = "linux";   Arch = "amd64"; Out = "$AppName-linux-amd64" },
    @{ OS = "darwin";  Arch = "amd64"; Out = "$AppName-macos-amd64" },
    @{ OS = "darwin";  Arch = "arm64"; Out = "$AppName-macos-arm64" }
)

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
