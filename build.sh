#!/usr/bin/env bash
# 一键交叉编译脚本（Linux / macOS）
# 产物输出到 dist/ 目录，使用 -ldflags "-s -w" 减小体积。

set -euo pipefail

APP_NAME="aceshare"
DIST_DIR="dist"

# 版本信息：优先用第一个命令行参数，其次用 git 描述，最后回退到 v0.0.0。
VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo v0.0.0)}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_TIME="$(date +%Y-%m-%d)"

echo "版本：${VERSION}  提交：${COMMIT}  构建时间：${BUILD_TIME}"

LDFLAGS="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}"

# 目标平台： "GOOS GOARCH 输出文件名"
TARGETS=(
  "windows amd64 ${APP_NAME}-windows-amd64.exe"
  "linux   amd64 ${APP_NAME}-linux-amd64"
  "darwin  amd64 ${APP_NAME}-macos-amd64"
  "darwin  arm64 ${APP_NAME}-macos-arm64"
)

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

# 生成 Windows exe 图标资源（.syso）。Go 编译 Windows 目标时会自动链接它。
# 使用一次性工具 rsrc，不会加入项目依赖；非 Windows 目标会自动忽略该文件。
if [ -f "logo.ico" ] && [ ! -f "rsrc_windows.syso" ]; then
  echo "生成 exe 图标资源 rsrc_windows.syso"
  go run github.com/akavel/rsrc@latest -ico logo.ico -o rsrc_windows.syso || \
    echo "警告：生成图标资源失败，将编译无图标版本"
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
