#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
version=$(cat VERSION)
mkdir -p dist
for arch in arm64 amd64; do
  name="runtime-status_${version}_darwin_${arch}"
  stage=$(mktemp -d)
  CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$stage/runtime-status" ./cmd/runtime-status
  cp LICENSE NOTICE README.md install.sh "$stage/"
  cp -R LICENSES "$stage/"
  cp -R docs "$stage/"
  cp -R assets "$stage/"
  mkdir -p "$stage/internal/telemetry"
  cp internal/telemetry/binding_darwin.go "$stage/internal/telemetry/"
  tar -czf "dist/$name.tar.gz" -C "$stage" runtime-status LICENSE NOTICE README.md install.sh LICENSES internal docs assets
  rm -rf "$stage"
done
(cd dist && shasum -a 256 "runtime-status_${version}_darwin_"*.tar.gz >"checksums_${version}.txt")
