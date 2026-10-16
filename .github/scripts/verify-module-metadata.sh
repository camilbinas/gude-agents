#!/usr/bin/env bash
# Verifies the dependency metadata of every published module before a release
# is committed or tagged.
#
# Usage: verify-module-metadata.sh <published-modules.txt> <published-module-paths.txt>
#
#   published-modules.txt       one module directory per line, relative to the
#                               repository root ("." is the root module)
#   published-module-paths.txt  one module path per line for the same modules
#
# It fails when, for any published module:
#
#   1. go.mod requires an internal module path that is not a published module
#      of this repository (a stale reference to a removed or renamed module);
#   2. a package of the module imports another published module that go.mod
#      does not require (a missing internal reference). Test-only imports of
#      sibling modules are tolerated, because those tests run through go.work
#      (see "Verify workspace modules"); they only exempt the module's test
#      dependencies from check 3. Packages are listed with local replacements,
#      not the workspace, so unpublished release versions never resolve;
#   3. the module cannot be loaded with a complete go.sum. This is checked in
#      read-only mode with every internal module replaced by its local
#      directory, so that unpublished release versions never need to resolve
#      but every external dependency must already have its checksums. Package
#      and test dependencies are both checked unless the tests need sibling
#      modules that go.mod does not declare.
#
# The check never edits a tracked file: the replacements live in a temporary
# modfile.
set -euo pipefail

modules_file=${1:?usage: verify-module-metadata.sh <published-modules.txt> <published-module-paths.txt>}
paths_file=${2:?usage: verify-module-metadata.sh <published-modules.txt> <published-module-paths.txt>}
root_module=${ROOT_MODULE:?ROOT_MODULE must name the repository root module path}
repo_root=$PWD

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# module path -> absolute directory, for every published module.
declare -A dir_of=()
while read -r module_dir; do
  path=$(cd "$module_dir" && go mod edit -json | jq -r '.Module.Path')
  dir_of["$path"]="$repo_root/$module_dir"
done < "$modules_file"

errors=()

while read -r module_dir; do
  module_json=$(cd "$module_dir" && go mod edit -json)
  self=$(jq -r '.Module.Path' <<< "$module_json")

  mapfile -t required < <(jq -r '.Require[]?.Path' <<< "$module_json")

  # 1. Stale internal references.
  for path in "${required[@]}"; do
    case "$path" in
      "$root_module" | "$root_module"/*)
        if [ -z "${dir_of[$path]:-}" ]; then
          errors+=("$module_dir requires $path, which is not a published module of this repository")
        fi
        ;;
    esac
  done

  # 2. Internal modules that are imported but not required. Packages are listed
  # with a temporary modfile that requires and locally replaces every internal
  # module, so release versions that are not tagged yet never need to resolve.
  # The workspace cannot be used here: after the release rewrite go.mod points
  # at unpublished versions that the go command would try to fetch.
  work="$tmp/$(printf '%s' "$module_dir" | tr '/.' '__')"
  mkdir -p "$work/all"
  cp "$module_dir/go.mod" "$work/all/go.mod"
  if [ -f "$module_dir/go.sum" ]; then
    cp "$module_dir/go.sum" "$work/all/go.sum"
  else
    : > "$work/all/go.sum"
  fi
  all_args=()
  for path in "${!dir_of[@]}"; do
    [ "$path" = "$self" ] && continue
    all_args+=("-replace=${path}=${dir_of[$path]}")
    if ! printf '%s\n' "${required[@]}" | grep -qxF "$path"; then
      all_args+=("-require=${path}@v0.0.0-00010101000000-000000000000")
    fi
  done
  if [ "${#all_args[@]}" -gt 0 ]; then
    go mod edit -modfile="$work/all/go.mod" "${all_args[@]}"
  fi
  list_modules() {
    (cd "$module_dir" && GOWORK=off GOFLAGS=-mod=mod GOTOOLCHAIN=local \
      go list -modfile="$work/all/go.mod" -deps "$@" \
      -f '{{if .Module}}{{.Module.Path}}{{end}}' ./... 2>"$tmp/list.err") | sort -u
  }
  if ! used=$(list_modules); then
    errors+=("$module_dir: cannot list imports: $(head -c 600 "$tmp/list.err" | tr '\n' ' ')")
    continue
  fi
  if ! used_with_tests=$(list_modules -test); then
    errors+=("$module_dir: cannot list test imports: $(head -c 600 "$tmp/list.err" | tr '\n' ' ')")
    continue
  fi
  check_test_closure=true
  while read -r path; do
    [ -n "$path" ] && [ "$path" != "$self" ] || continue
    [ -n "${dir_of[$path]:-}" ] || continue
    if printf '%s\n' "${required[@]}" | grep -qxF "$path"; then
      continue
    fi
    if printf '%s\n' "$used" | grep -qxF "$path"; then
      errors+=("$module_dir imports $path but go.mod does not require it")
    else
      check_test_closure=false
      echo "notice: $module_dir tests import $path without requiring it; test dependency checksums are not checked"
    fi
  done <<< "$used_with_tests"
  # 3. Complete checksums, resolved with local replacements for every internal
  # module so only external dependencies are looked up.
  mkdir -p "$work"
  cp "$module_dir/go.mod" "$work/go.mod"
  if [ -f "$module_dir/go.sum" ]; then
    cp "$module_dir/go.sum" "$work/go.sum"
  else
    : > "$work/go.sum"
  fi
  args=()
  for path in "${!dir_of[@]}"; do
    [ "$path" = "$self" ] && continue
    args+=("-replace=${path}=${dir_of[$path]}")
  done
  if [ "${#args[@]}" -gt 0 ]; then
    go mod edit -modfile="$work/go.mod" "${args[@]}"
  fi
  test_flag=()
  if [ "$check_test_closure" = true ]; then test_flag=(-test); fi
  if ! (cd "$module_dir" && GOWORK=off GOFLAGS=-mod=readonly GOTOOLCHAIN=local \
      go list -modfile="$work/go.mod" -deps "${test_flag[@]}" ./... >/dev/null 2>"$tmp/sum.err"); then
    errors+=("$module_dir: go.mod/go.sum are incomplete: $(head -c 600 "$tmp/sum.err" | tr '\n' ' ')")
  fi
done < "$modules_file"

if [ "${#errors[@]}" -gt 0 ]; then
  echo "::error::Module metadata verification failed:"
  printf '  %s\n' "${errors[@]}"
  exit 1
fi
echo "Module metadata verified for $(wc -l < "$modules_file" | tr -d ' ') published modules."
