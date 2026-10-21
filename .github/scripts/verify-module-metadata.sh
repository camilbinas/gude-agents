#!/usr/bin/env bash
# Usage: verify-module-metadata.sh <version> <proxy-dir>
#
# Checks the prepared release checkout exactly as a consumer outside the
# workspace would load each published module: GOWORK=off, no replacements, and
# a read-only go.mod/go.sum. Internal requirements must be exactly v<version>
# and resolve through the local proxy that seal-module-sums.sh filled, because
# those tags do not exist yet. No modfile or other shortcut is used, so an
# incomplete go.mod or go.sum fails here instead of after publication.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
version=${1:?usage: verify-module-metadata.sh <version> <proxy-dir>}
proxy_dir=$(cd "${2:?usage: verify-module-metadata.sh <version> <proxy-dir>}" && pwd)

errors=()
for dir in "${PUBLISHED_DIRS[@]}"; do
  json=$(cd "$REPO_ROOT/$dir" && go mod edit -json)
  while read -r path required_version; do
    [ -n "$path" ] || continue
    case "$path" in
      "$ROOT_MODULE" | "$ROOT_MODULE"/*)
        if [ -z "${DIR_OF[$path]:-}" ]; then
          errors+=("$dir requires $path, which is not a published module of this repository")
        elif [ "$required_version" != "v$version" ]; then
          errors+=("$dir requires $path@$required_version, expected v$version")
        fi
        ;;
    esac
  done < <(jq -r '.Require[]? | "\(.Path) \(.Version)"' <<< "$json")
  retained=$(jq -r --arg root "$ROOT_MODULE" '.Replace[]?.Old.Path | select(. == $root or startswith($root + "/"))' <<< "$json")
  if [ -n "$retained" ]; then
    errors+=("$dir keeps internal replace directives, which consumers ignore: $retained")
  fi
done
if [ "${#errors[@]}" -gt 0 ]; then
  echo "::error::Prepared module requirements are wrong:" >&2
  printf '  %s\n' "${errors[@]}" >&2
  exit 1
fi

export GOWORK=off GOFLAGS=-mod=readonly GOTOOLCHAIN=local
export GOPROXY="file://$proxy_dir,https://proxy.golang.org,direct"
export GONOSUMDB="$ROOT_MODULE"
failed=()
for dir in "${PUBLISHED_DIRS[@]}"; do
  echo "::group::standalone $dir"
  go_checks "$dir" || failed+=("$dir")
  echo "::endgroup::"
done
if [ "${#failed[@]}" -gt 0 ]; then
  echo "::error::Modules that cannot be built and tested on their own (GOWORK=off): ${failed[*]}" >&2
  exit 1
fi
echo "Verified ${#PUBLISHED_DIRS[@]} published modules at v$version."
