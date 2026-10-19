#!/bin/sh
# Podman-only, rootless, offline, RAM-only. Usage: ./build.sh [deps|test|build|all]
#   deps  : `go mod download` with network into $GOMODCACHE_HOST (the only step with network; run after adding a dependency AND vetting it)
#   test  : go vet + go test (no network, read-only source, tmpfs)
#   build : static binaries into ./dist (linux/amd64 and linux/arm64, darwin/arm64)
set -eu
cd "$(dirname "$0")"
MOD=${GOMODCACHE_HOST:-$HOME/go/pkg/mod}; IMG=docker.io/library/golang:1.25; mkdir -p "$MOD" dist
base() { podman run --rm "$@"; }
case "${1:-all}" in
deps) base --network=host --tmpfs /tmp:exec,size=2g -e GOFLAGS=-mod=mod -e GOCACHE=/tmp/gc -e GOMODCACHE=/mod -v "$MOD":/mod:rw -v "$PWD":/src:rw -w /src $IMG sh -c 'go mod tidy && go mod download' ;;
test|all)
  base --network=none --read-only --tmpfs /tmp:exec,size=2g -e GOFLAGS=-mod=mod -e GOPROXY=off -e CGO_ENABLED=0 -e GOCACHE=/tmp/gc -e GOMODCACHE=/mod -v "$MOD":/mod:ro -v "$PWD":/src:ro -w /src $IMG sh -c 'go vet ./... && go test ./... -count=1'
  [ "${1:-all}" = test ] || set -- build ;;&
build)
  for t in linux/amd64 linux/arm64 darwin/arm64; do
    base --network=none --tmpfs /tmp:exec,size=2g -e GOFLAGS=-mod=mod -e GOPROXY=off -e CGO_ENABLED=0 -e GOOS=${t%/*} -e GOARCH=${t#*/} -e GOCACHE=/tmp/gc -e GOMODCACHE=/mod -v "$MOD":/mod:ro -v "$PWD":/src:ro -v "$PWD/dist":/out -w /src $IMG \
      go build -trimpath -ldflags="-s -w -X github.com/MQ37/nod/internal/version.Version=${VERSION:-dev}" -o /out/nod-${t%/*}-${t#*/} ./cmd/nod
  done; sha256sum dist/nod-* ;;
esac
