#!/bin/sh
# Install medic from GitHub releases.
#   curl -fsSL https://raw.githubusercontent.com/Rogsiel/medic/main/install.sh | sh
# Env: MEDIC_BASE_URL (mirror serving the release assets), MEDIC_VERSION (default: latest, e.g. v0.2.0), MEDIC_INSTALL_DIR (default: /usr/local/bin if writable, else ~/.local/bin)
set -eu

repo="Rogsiel/medic"
version="${MEDIC_VERSION:-latest}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) echo "medic install: unsupported OS '$os' (download a release from https://github.com/$repo/releases)" >&2; exit 1 ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "medic install: unsupported architecture '$arch'" >&2; exit 1 ;;
esac

if [ -n "${MEDIC_BASE_URL:-}" ]; then
  base="$MEDIC_BASE_URL"
elif [ "$version" = "latest" ]; then
  base="https://github.com/$repo/releases/latest/download"
else
  base="https://github.com/$repo/releases/download/$version"
fi
asset="medic_${os}_${arch}.tar.gz"

dir="${MEDIC_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
  if [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi
fi
mkdir -p "$dir"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fetch() {
  if command -v curl >/dev/null 2>&1; then curl -fsSL -o "$2" "$1"; else wget -qO "$2" "$1"; fi
}
fetch "$base/$asset" "$tmp/$asset"
fetch "$base/checksums.txt" "$tmp/checksums.txt"

want=$(grep " $asset\$" "$tmp/checksums.txt" | cut -d' ' -f1)
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$asset" | cut -d' ' -f1)
else
  got=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "medic install: checksum mismatch for $asset" >&2
  exit 1
fi

tar -xzf "$tmp/$asset" -C "$tmp" medic
install -m 0755 "$tmp/medic" "$dir/medic"
echo "installed $("$dir/medic" --version) to $dir/medic"
case ":$PATH:" in *":$dir:"*) ;; *) echo "note: $dir is not on your PATH" >&2 ;; esac
