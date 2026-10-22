#!/usr/bin/env bash
# 发布新版本后同步 AUR 的 kcxy-wifi-bin。
#
# 用法：./aur/update-aur.sh 1.0.2
# 前提：GitHub Release v1.0.2 已发布，且本机已配置 AUR SSH 密钥。
set -euo pipefail

pkgver="${1:?用法: $0 <版本号，例如 1.0.2>}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
aur_dir="${AUR_DIR:-$repo_root/.aur-kcxy-wifi-bin}"
base="https://github.com/Become-ILLUSORY/kcxy-wifi/releases/download/v${pkgver}"

assets=(
  "x86_64:kcxy-wifi-linux-amd64.tar.gz"
  "aarch64:kcxy-wifi-linux-arm64.tar.gz"
  "armv7h:kcxy-wifi-linux-armv7.tar.gz"
)

echo "==> 计算 v${pkgver} 各架构校验和"
declare -A sums
for entry in "${assets[@]}"; do
  arch="${entry%%:*}"
  file="${entry##*:}"
  tmp="$(mktemp)"
  curl -fsSL -o "$tmp" "$base/$file"
  sums["$arch"]="$(sha256sum "$tmp" | cut -d' ' -f1)"
  rm -f "$tmp"
  echo "  $arch -> ${sums[$arch]}"
done

echo "==> 更新 $repo_root/aur/PKGBUILD"
sed -i "s/^pkgver=.*/pkgver=${pkgver}/" "$repo_root/aur/PKGBUILD"
for arch in "${!sums[@]}"; do
  sed -i "s/^sha256sums_${arch}=.*/sha256sums_${arch}=('${sums[$arch]}')/" "$repo_root/aur/PKGBUILD"
done

if [[ -d "$aur_dir/.git" ]]; then
  git -C "$aur_dir" pull --rebase --quiet
else
  git clone --quiet ssh://aur@aur.archlinux.org/kcxy-wifi-bin.git "$aur_dir"
fi

cp "$repo_root/aur/PKGBUILD" "$aur_dir/PKGBUILD"
(cd "$aur_dir" && makepkg --printsrcinfo > .SRCINFO)
git -C "$aur_dir" add PKGBUILD .SRCINFO
git -C "$aur_dir" commit -m "更新到 ${pkgver}-1"
git -C "$aur_dir" push origin master

echo "==> 已推送 kcxy-wifi-bin ${pkgver}-1 到 AUR"
