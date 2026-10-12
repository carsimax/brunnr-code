#!/usr/bin/env bash
# Install brunnr-code from GitHub Releases.
#
#   curl -fsSL https://raw.githubusercontent.com/carsimax/brunnr-code/main/scripts/install.sh | bash
#
# Env: BRUNNR_CODE_VERSION=v1.0.0 to pin a version, BRUNNR_CODE_INSTALL_DIR to override the target dir.
set -euo pipefail

OWNER="carsimax"
REPO="brunnr-code"
BINARY="brunnr-code"

fatal() { echo "error: $*" >&2; exit 1; }

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fatal "unsupported OS: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) fatal "unsupported architecture: $(uname -m)" ;;
esac

tag="${BRUNNR_CODE_VERSION:-}"
if [ -z "$tag" ]; then
  tag="$(curl -fsSL "https://api.github.com/repos/${OWNER}/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
  [ -n "$tag" ] || fatal "could not resolve latest release"
fi
version="${tag#v}"
archive="${BINARY}_${version}_${os}_${arch}.tar.gz"
base="https://github.com/${OWNER}/${REPO}/releases/download/${tag}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading ${archive} (${tag})..."
curl -fsSL -o "$tmp/$archive" "$base/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"

expected="$(awk -v f="$archive" '$2 == f {print $1}' "$tmp/checksums.txt")"
[ -n "$expected" ] || fatal "${archive} not listed in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$archive" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')"
fi
[ "$expected" = "$actual" ] || fatal "checksum mismatch for ${archive}"

tar -xzf "$tmp/$archive" -C "$tmp" "$BINARY"

dir="${BRUNNR_CODE_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
  if [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi
fi
mkdir -p "$dir"
install -m 0755 "$tmp/$BINARY" "$dir/$BINARY"
echo "Installed ${BINARY} ${tag} to ${dir}/${BINARY}"

case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "note: ${dir} is not in your PATH; add it to use '${BINARY}' directly." ;;
esac
