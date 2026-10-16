#!/usr/bin/env bash
# Completes go.mod/go.sum for the release version of every published module.
#
# Usage: seal-module-sums.sh <published-modules.txt> <published-module-paths.txt>
#
# Environment:
#   ROOT_MODULE       module path of the repository root module
#   MODULE_PROXY_DIR  directory that receives a local file module proxy
#                     containing every published module at the release version
#
# After the release rewrite, go.mod names an internal version that is not
# tagged yet, so `go mod tidy` cannot fetch it and go.sum would miss the
# checksum of every internal requirement. That makes a module impossible to
# build on its own (GOWORK=off go test ./...). This script:
#
#   1. orders the published modules so every module follows the internal
#      modules it requires (the internal requirement graph must be acyclic);
#   2. runs `go mod tidy` for a module with the local proxy in front of the
#      public one, so internal versions resolve from this checkout and every
#      external module resolves from the public proxy;
#   3. publishes the finished module (go.mod and go.sum included) into the
#      local proxy, so modules that require it can resolve it in turn.
#
# Internal modules are excluded from the public checksum database lookup
# through GONOSUMDB because their release version is not published yet. The
# checksums written here are those of the same files that the release commit
# tags; the public proxy serves identical content for the tags.
set -euo pipefail
modules_file=${1:?usage: seal-module-sums.sh <published-modules.txt> <published-module-paths.txt>}
paths_file=${2:?usage: seal-module-sums.sh <published-modules.txt> <published-module-paths.txt>}
root_module=${ROOT_MODULE:?ROOT_MODULE must name the repository root module path}
proxy_dir=${MODULE_PROXY_DIR:?MODULE_PROXY_DIR must name the local module proxy directory}
repo_root=$PWD
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
mkdir -p "$proxy_dir"
proxy_dir=$(cd "$proxy_dir" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Build the proxy publisher from a throwaway module so the repository carries
# no tool dependency.
mkdir -p "$tmp/modproxy"
cp "$script_dir/modproxy/main.go" "$tmp/modproxy/main.go"
(
  cd "$tmp/modproxy"
  go mod init modproxy >/dev/null 2>&1
  GOWORK=off GOFLAGS=-mod=mod go get golang.org/x/mod@v0.27.0 >/dev/null 2>&1
  GOWORK=off GOFLAGS=-mod=mod go build -o "$tmp/modproxy-bin" .
)

declare -A path_of=()   # module dir -> module path
declare -A dir_of=()    # module path -> module dir
declare -A deps_of=()   # module dir -> space separated internal module paths
while read -r module_dir; do
  json=$(cd "$module_dir" && go mod edit -json)
  path=$(jq -r '.Module.Path' <<< "$json")
  path_of["$module_dir"]=$path
  dir_of["$path"]=$module_dir
done < "$modules_file"
while read -r module_dir; do
  json=$(cd "$module_dir" && go mod edit -json)
  deps=""
  while read -r required; do
    [ -n "$required" ] && [ -n "${dir_of[$required]:-}" ] && deps="$deps $required"
  done < <(jq -r '.Require[]?.Path' <<< "$json")
  deps_of["$module_dir"]=$deps
done < "$modules_file"

# Kahn's algorithm: repeatedly take the modules whose internal requirements
# have all been sealed.
declare -A done_dirs=()
order=()
remaining=$(wc -l < "$modules_file" | tr -d ' ')
while [ "${#order[@]}" -lt "$remaining" ]; do
  progressed=false
  while read -r module_dir; do
    [ -z "${done_dirs[$module_dir]:-}" ] || continue
    ready=true
    for dep in ${deps_of[$module_dir]}; do
      [ -n "${done_dirs[${dir_of[$dep]}]:-}" ] || ready=false
    done
    if [ "$ready" = true ]; then
      order+=("$module_dir")
      done_dirs["$module_dir"]=1
      progressed=true
    fi
  done < "$modules_file"
  if [ "$progressed" = false ]; then
    echo "::error::The internal module requirements contain a cycle; checksums cannot be sealed:"
    while read -r module_dir; do
      [ -n "${done_dirs[$module_dir]:-}" ] || echo "  $module_dir requires:${deps_of[$module_dir]}"
    done < "$modules_file"
    exit 1
  fi
done

# The version every internal requirement names is the version published to
# the local proxy.
version=""
for module_dir in "${order[@]}"; do
  json=$(cd "$module_dir" && go mod edit -json)
  while read -r v; do
    [ -n "$v" ] || continue
    if [ -z "$version" ]; then version=$v; fi
    if [ "$v" != "$version" ]; then
      echo "::error::$module_dir requires internal modules at both $version and $v"
      exit 1
    fi
  done < <(jq -r --arg root "$root_module" \
    '.Require[]? | select(.Path == $root or (.Path | startswith($root + "/"))) | .Version' <<< "$json")
done
if [ -z "$version" ]; then
  echo "::error::No internal module requirement found; nothing to seal."
  exit 1
fi

export GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=mod
export GOPROXY="file://$proxy_dir,https://proxy.golang.org,direct"
export GONOSUMDB="$root_module"
for module_dir in "${order[@]}"; do
  path=${path_of[$module_dir]}
  (cd "$module_dir" && go mod tidy)
  "$tmp/modproxy-bin" -repo "$repo_root" -dir "$module_dir" -path "$path" -version "$version" -proxy "$proxy_dir"
  echo "sealed $path@$version"
done
