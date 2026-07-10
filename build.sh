#!/usr/bin/env bash
# 一键交叉编译脚本（Linux / macOS）
# 产物输出到 dist/ 目录，使用 -ldflags "-s -w" 减小体积。
#
# 用法示例（位置参数：平台 [版本号]）：
#   ./build.sh                 # 编译全部平台（默认）
#   ./build.sh windows         # 只编译 Windows exe
#   ./build.sh mac             # 只编译 macOS（amd64 + arm64）
#   ./build.sh linux           # 只编译 Linux
#   ./build.sh all v1.2.0      # 全部平台，指定版本号
#   ./build.sh windows v1.2.0  # 只编译 Windows，指定版本号

set -euo pipefail

APP_NAME="aceshare"
DIST_DIR="dist"

# 第一个参数：目标平台（all/windows/linux/mac/darwin），缺省为 all。
PLATFORM="${1:-all}"

# 版本信息：优先用第二个命令行参数，其次用 git 描述，最后回退到 v0.0.0。
VERSION="${2:-$(git describe --tags --always --dirty 2>/dev/null || echo v0.0.0)}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_TIME="$(date +%Y-%m-%d)"

echo "平台：${PLATFORM}  版本：${VERSION}  提交：${COMMIT}  构建时间：${BUILD_TIME}"

LDFLAGS="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}"

# 全部可选目标平台： "GOOS GOARCH 输出文件名"
ALL_TARGETS=(
  "windows amd64 ${APP_NAME}-windows-amd64.exe"
  "windows arm64 ${APP_NAME}-windows-arm64.exe"
  "linux   amd64 ${APP_NAME}-linux-amd64"
  "darwin  amd64 ${APP_NAME}-macos-amd64"
  "darwin  arm64 ${APP_NAME}-macos-arm64"
)

# 根据平台参数筛选要编译的目标。
TARGETS=()
case "$(echo "$PLATFORM" | tr '[:upper:]' '[:lower:]')" in
  all)
    TARGETS=("${ALL_TARGETS[@]}")
    ;;
  windows|win)
    TARGETS=(
      "windows amd64 ${APP_NAME}-windows-amd64.exe"
      "windows arm64 ${APP_NAME}-windows-arm64.exe"
    )
    ;;
  linux)
    TARGETS=("linux amd64 ${APP_NAME}-linux-amd64")
    ;;
  mac|macos|darwin)
    TARGETS=(
      "darwin amd64 ${APP_NAME}-macos-amd64"
      "darwin arm64 ${APP_NAME}-macos-arm64"
    )
    ;;
  *)
    echo "未知平台：${PLATFORM}（可选：all / windows / linux / mac）" >&2
    exit 1
    ;;
esac

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

# 生成 Windows exe 图标资源（.syso）。按架构分别生成，Go 会按文件名自动选用。
# 使用一次性工具 rsrc，不会加入项目依赖；非 Windows 目标会自动忽略这些文件。
# 旧的无架构后缀文件会在 arm64 链接时报错，若存在则删除。
rm -f rsrc_windows.syso
if [ -f "logo.ico" ]; then
  for arch in amd64 arm64; do
    syso="rsrc_windows_${arch}.syso"
    if [ ! -f "$syso" ]; then
      echo "生成 exe 图标资源 ${syso}"
      go run github.com/akavel/rsrc@latest -arch "$arch" -ico logo.ico -o "$syso" || \
        echo "警告：生成 ${syso} 失败，对应架构将编译无图标版本"
    fi
  done
fi

# 禁用 CGO，保证静态、零依赖单文件。
export CGO_ENABLED=0

for entry in "${TARGETS[@]}"; do
  # shellcheck disable=SC2086
  set -- $entry
  goos="$1"; goarch="$2"; out="$3"
  echo "编译 ${goos}/${goarch} -> ${DIST_DIR}/${out}"
  GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "$LDFLAGS" -o "${DIST_DIR}/${out}" .
done

echo ""
echo "全部完成，产物位于 ${DIST_DIR}/ ："
ls -lh "$DIST_DIR"
