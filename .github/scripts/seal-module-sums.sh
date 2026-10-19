#!/usr/bin/env bash
# Usage: seal-module-sums.sh <version> <proxy-dir>
#
# Completes go.sum for every published module at the release version, before
# that version's tags exist.
#
# Why this is needed: after "prepare", each go.mod requires its siblings at
# v<version>, a version no module proxy serves yet. Without checksums for those
# requirements in go.sum, `GOWORK=off go test ./...` fails inside any module,
# and the public proxy cannot be asked to compute them. So this script
# publishes each module into a local file proxy (modproxy builds the same zip
# the public proxy would serve for the tag), then runs `go mod tidy` against
# that proxy so go.sum records the checksums. Modules are processed in
# dependency order because a module's zip contains its finished go.sum, and
# that hash is what its dependents record.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
version=${1:?usage: seal-module-sums.sh <version> <proxy-dir>}
proxy_dir=${2:?usage: seal-module-sums.sh <version> <proxy-dir>}
mkdir -p "$proxy_dir"
proxy_dir=$(cd "$proxy_dir" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# modproxy lives in the repo as a single file; build it inside a throwaway
# module so the repository itself carries no tool dependency.
mkdir -p "$tmp/modproxy"
cp "$REPO_ROOT/.github/scripts/modproxy/main.go" "$tmp/modproxy/main.go"
(
  cd "$tmp/modproxy"
  go mod init modproxy >/dev/null 2>&1
  GOWORK=off GOFLAGS=-mod=mod go get golang.org/x/mod@v0.27.0 >/dev/null 2>&1
  GOWORK=off GOFLAGS=-mod=mod go build -o "$tmp/modproxy-bin" .
)

# Kahn's algorithm over the internal requirements: a module is sealed once all
# the published modules it requires have been.
declare -A deps_of=() sealed=()
for dir in "${PUBLISHED_DIRS[@]}"; do
  deps=""
  while read -r required; do
    [ -n "${DIR_OF[$required]:-}" ] && deps="$deps ${DIR_OF[$required]}"
  done < <(cd "$REPO_ROOT/$dir" && go mod edit -json | jq -r '.Require[]?.Path')
  deps_of["$dir"]=$deps
done
order=()
while [ "${#order[@]}" -lt "${#PUBLISHED_DIRS[@]}" ]; do
  progressed=false
  for dir in "${PUBLISHED_DIRS[@]}"; do
    [ -z "${sealed[$dir]:-}" ] || continue
    ready=true
    for dep in ${deps_of[$dir]}; do
      [ -n "${sealed[$dep]:-}" ] || ready=false
    done
    if [ "$ready" = true ]; then
      order+=("$dir")
      sealed["$dir"]=1
      progressed=true
    fi
  done
  if [ "$progressed" = false ]; then
    echo "::error::The internal module requirements contain a cycle; checksums cannot be sealed:" >&2
    for dir in "${PUBLISHED_DIRS[@]}"; do
      [ -n "${sealed[$dir]:-}" ] || echo "  $dir requires:${deps_of[$dir]}" >&2
    done
    exit 1
  fi
done

# Internal versions resolve from the local proxy; external modules come from
# the public one. GONOSUMDB keeps unreleased internal versions out of the
# checksum database lookup.
export GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=mod
export GOPROXY="file://$proxy_dir,https://proxy.golang.org,direct"
export GONOSUMDB="$ROOT_MODULE"
for dir in "${order[@]}"; do
  (cd "$REPO_ROOT/$dir" && go mod tidy)
  "$tmp/modproxy-bin" -repo "$REPO_ROOT" -dir "$dir" -path "${PATH_OF[$dir]}" -version "v$version" -proxy "$proxy_dir"
  echo "sealed ${PATH_OF[$dir]}@v$version"
done
