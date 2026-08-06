#!/bin/sh
# ViPanel 一键安装：探测架构、下载对应产物、**校验 sha256**、解压、装。
#
#   curl -fsSL https://raw.githubusercontent.com/Arrosam/ViPanel/main/scripts/get.sh | sudo sh
#
# 校验不是可选步骤。这个脚本要把四个以 root 运行的二进制放进 /usr/local/bin，
# 不比对校验和就等于「网络上给什么就装什么」。
set -e

REPO="${REPO:-Arrosam/ViPanel}"
VERSION="${VERSION:-latest}"

die() { echo "✗ $1" >&2; exit 1; }
ok()  { echo "✓ $1"; }

[ "$(id -u)" = "0" ] || die "需要 root：agent 要以 root 运行才能管这台机器"

case "$(uname -s)" in
    Linux) ;;
    *) die "只支持 Linux（当前 $(uname -s)）" ;;
esac

case "$(uname -m)" in
    x86_64|amd64)  ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) die "不支持的架构 $(uname -m)（只出 amd64 / arm64）" ;;
esac
ok "架构 $ARCH"

for c in curl tar sha256sum; do
    command -v "$c" >/dev/null || die "缺少 $c"
done

# claude 不在的话装完也用不了，不如现在就说清楚
command -v claude >/dev/null || cat <<'WARN'
⚠ 没找到 claude。装完之后控制台会起不了会话。
  先装：npm i -g @anthropic-ai/claude-code
  （agent 直接拉起宿主机上的 claude，不进容器——进了容器它就管不了这台机器）
WARN

if [ "$VERSION" = "latest" ]; then
    VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
        | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
    [ -n "$VERSION" ] || die "查不到最新版本，手工指定：VERSION=v0.1.0 sh get.sh"
fi
ok "版本 $VERSION"

NAME="vipanel-${VERSION}-linux-${ARCH}"
BASE="https://github.com/$REPO/releases/download/$VERSION"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "下载 $NAME.tar.gz …"
curl -fsSL -o "$TMP/$NAME.tar.gz" "$BASE/$NAME.tar.gz" || die "下载失败"
curl -fsSL -o "$TMP/SHA256SUMS"   "$BASE/SHA256SUMS"   || die "下载校验和失败"

( cd "$TMP" && grep " $NAME.tar.gz\$" SHA256SUMS | sha256sum -c - ) \
    || die "sha256 校验不通过——不要安装这份产物"
ok "sha256 校验通过"

tar -C "$TMP" -xzf "$TMP/$NAME.tar.gz"
ok "已解压"

echo
echo "==> 开始安装。装之前的最后一次提醒："
echo "    能打开 ViPanel 控制台的人，约等于能在这台机器上以 root 执行任意命令。"
echo "    详见解压目录里的 docs/trust-model.md"
echo
exec sh "$TMP/$NAME/scripts/install.sh"
