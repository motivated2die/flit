#!/bin/sh
# Runs inside the Go Linux build container to cross-compile flit.exe for Windows.
# Invoked by scripts/build-release-docker.cmd and scripts/build-release-docker.ps1.
set -eu

: "${VERSION:?VERSION is required}"
: "${WIN_VERSION:?WIN_VERSION is required}"
: "${COMMIT:=unknown}"
: "${OUT_NAME:=flit.exe}"
: "${LDFLAGS:=-H=windowsgui -s -w -X github.com/motivated2die/flit/internal/app.Version=${VERSION}}"
: "${SKIP_VET:=0}"

echo "== staging source in /work"
mkdir -p /work /out
tar -C /src \
    --exclude='./.git' \
    --exclude='./.cache' \
    --exclude='./models' \
    --exclude='./references' \
    --exclude='./testdata' \
    --exclude='./*.exe' \
    -cf - . | tar -C /work -xf -
cd /work

if [ ! -f internal/engine/native/files/transcribe.dll ]; then
    echo "== fetching pinned transcribe.cpp engine DLLs"
    BUNDLE_NAME=$(sed -n 's/.*BundleName[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/engine/native/native.go)
    BUNDLE_URL=$(sed -n 's/.*BundleURL[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/engine/native/native.go)"$BUNDLE_NAME"
    BUNDLE_SHA=$(sed -n 's/.*BundleSHA256[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/engine/native/native.go)
    mkdir -p /tmp/engine internal/engine/native/files
    curl -fsSL -o "/tmp/engine/$BUNDLE_NAME" "$BUNDLE_URL"
    echo "$BUNDLE_SHA  /tmp/engine/$BUNDLE_NAME" | sha256sum -c -
    tar -xzf "/tmp/engine/$BUNDLE_NAME" -C /tmp/engine --strip-components=1
    cp /tmp/engine/contract.json internal/engine/native/files/
    if [ -d /tmp/engine/licenses ]; then
        cp -r /tmp/engine/licenses internal/engine/native/files/
    fi
    find /tmp/engine -maxdepth 1 -type f -iname '*.dll' ! -iname '*vulkan*' -exec cp {} internal/engine/native/files/ \;
    rm -rf /tmp/engine
else
    echo "== engine DLLs already present in workspace"
fi

if [ "$SKIP_VET" != "1" ]; then
    echo "== go vet (windows/amd64)"
    GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...
fi

echo "== resources (icon, manifest, version $WIN_VERSION)"
go run github.com/tc-hib/go-winres@v0.3.3 make \
    --in winres/winres.json \
    --out cmd/flit/rsrc \
    --arch amd64 \
    --product-version "$WIN_VERSION" \
    --file-version "$WIN_VERSION"

echo "== build /out/$OUT_NAME (v$VERSION, commit $COMMIT)"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "$LDFLAGS" \
    -o "/out/$OUT_NAME" \
    ./cmd/flit

(cd /out && sha256sum "$OUT_NAME" > SHA256SUMS.txt)
cat /out/SHA256SUMS.txt
