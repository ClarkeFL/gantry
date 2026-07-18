#!/bin/sh
# local build: frontend + linux binaries into ./dist
set -e
cd "$(dirname "$0")"
(cd web && npm run build)
VERSION="$(git describe --tags --always 2>/dev/null || echo dev)"
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w -X main.version=$VERSION" -o dist/gantry-linux-amd64 .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "-s -w -X main.version=$VERSION" -o dist/gantry-linux-arm64 .
echo "built dist/gantry-linux-{amd64,arm64} ($VERSION)"
