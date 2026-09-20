#!/usr/bin/env bash
# Cross-compile vault for every supported platform into dist/.
#   ./build.sh            → version from the nearest git tag (or 0.0.0-dev)
#   ./build.sh 0.3.0      → explicit version
set -euo pipefail
cd "$(dirname "$0")"
VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo 0.0.0-dev)}"
LDFLAGS="-s -w -X github.com/shashb27/vault/internal/vault.Version=${VERSION}"
mkdir -p dist
for target in darwin/arm64 darwin/amd64 windows/amd64 windows/arm64 linux/amd64 linux/arm64; do
  GOOS="${target%/*}"; GOARCH="${target#*/}"
  out="dist/vault-${GOOS}-${GOARCH}"; [[ "$GOOS" == "windows" ]] && out="$out.exe"
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/vault
  echo "built $out"
done
( cd dist && shasum -a 256 vault-* > SHA256SUMS )
echo "version $VERSION"
