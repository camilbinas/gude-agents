#!/usr/bin/env bash
# Release mechanics for the lockstep multi-module release. The workflow only
# orders these commands; each one does a single stage.
#
#   release.sh choose-version [--version 1.9.0]
#   release.sh validate --version 1.9.0 [--mode release]
#   release.sh test
#   release.sh prepare  --version 1.9.0
#   release.sh seal     --version 1.9.0
#   release.sh verify   --version 1.9.0
#   release.sh plan     --version 1.9.0
#   release.sh commit   --version 1.9.0
#   release.sh tags     --version 1.9.0
#   release.sh push     --version 1.9.0
#   release.sh github-release --version 1.9.0
#
# The module list comes from .github/modules.txt (see lib.sh); nothing else
# discovers modules.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
cd "$REPO_ROOT"

SCRIPTS="$REPO_ROOT/.github/scripts"
RELEASE_BRANCH=master
# The sealed local proxy must survive from "seal" to "verify".
PROXY_DIR=${GUDE_RELEASE_PROXY:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/gude-release-proxy}

fail() { echo "::error::$*" >&2; exit 1; }

parse_args() {
  VERSION="" MODE=dry-run
  while [ $# -gt 0 ]; do
    case "$1" in
      --version) VERSION=${2:-}; shift 2 ;;
      --mode) MODE=${2:-}; shift 2 ;;
      *) fail "unknown argument: $1" ;;
    esac
  done
}

require_version() {
  [ -n "$VERSION" ] || fail "--version is required, for example --version 1.9.0"
}

# Only go.mod and go.sum may differ from the checked-out source after prepare.
changed_files() { git status --porcelain --untracked-files=all | sed -E 's/^.{3}//'; }

assert_only_module_files_changed() {
  local unexpected
  unexpected=$(changed_files | grep -vE '(^|/)go\.(mod|sum)$' || true)
  [ -z "$unexpected" ] || fail "files other than go.mod and go.sum changed:"$'\n'"$unexpected"
}

# ---------------------------------------------------------------------------

# Decides which version a run releases and writes version= and skip= to
# $GITHUB_OUTPUT. A manual run names its version. A push to master derives it
# from the conventional commits since the latest final tag: feat bumps minor;
# fix, refactor and perf bump patch. Breaking changes are never released
# automatically (a major version is a manual decision), and neither are
# commits that only touch chores, docs or tests.
cmd_choose_version() {
  local version="" skip=false reason="" latest range subject message bump=none
  if [ -n "$VERSION" ]; then
    version=$VERSION
  elif [[ "$(git log -1 --format=%s)" == "chore: release v"[0-9]* ]]; then
    skip=true reason="HEAD is a release commit, which never starts another release"
  else
    latest=$(git tag --list 'v[0-9]*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)
    [ -n "$latest" ] || fail "no final v<major>.<minor>.<patch> tag to bump from; run the workflow manually"
    range="$latest..HEAD"
    while IFS= read -r -d '' message; do
      subject=${message%%$'\n'*}
      [ -n "${subject//[[:space:]]/}" ] || continue
      if grep -qE '^[a-z]+(\([^)]*\))?!:' <<< "$subject" || grep -qE '^BREAKING[ -]CHANGE:' <<< "$message"; then
        bump=breaking
        break
      elif grep -qE '^feat(\([^)]*\))?:' <<< "$subject"; then
        bump=minor
      elif [ "$bump" = none ] && grep -qE '^(fix|refactor|perf)(\([^)]*\))?:' <<< "$subject"; then
        bump=patch
      fi
    done < <(git log -z --format=%B "$range"; printf '\0')
    local major minor patch
    IFS=. read -r major minor patch <<< "${latest#v}"
    case "$bump" in
      minor) version="$major.$((minor + 1)).0" ;;
      patch) version="$major.$minor.$((patch + 1))" ;;
      breaking) skip=true reason="a breaking change since $latest needs a manual release (run this workflow with the version you want)" ;;
      none) skip=true reason="no feat, fix, refactor or perf commits since $latest" ;;
    esac
  fi
  if [ "$skip" = true ]; then
    echo "::notice::No automatic release: $reason."
  else
    echo "Releasing v$version."
  fi
  if [ -n "${GITHUB_OUTPUT:-}" ]; then
    { echo "version=$version"; echo "skip=$skip"; } >> "$GITHUB_OUTPUT"
  else
    echo "version=$version skip=$skip"
  fi
}

cmd_validate() {
  require_version
  local semver='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
  case "$VERSION" in
    v*) fail "version must not start with 'v': use ${VERSION#v}" ;;
  esac
  [[ "$VERSION" =~ $semver ]] || fail "'$VERSION' is not a semantic version like 1.9.0 or 1.9.0-rc.1"
  # Module paths carry no /vN suffix, which Go requires from v2 on.
  [ "${VERSION%%.*}" -le 1 ] || fail "major version ${VERSION%%.*} needs /v${VERSION%%.*} module paths; only v0 and v1 are supported"
  [ "$MODE" = dry-run ] || [ "$MODE" = release ] || fail "mode must be dry-run or release, got '$MODE'"

  check_manifest
  check_tags_unused
  if [ "$MODE" = release ]; then
    [ "$(git branch --show-current)" = "$RELEASE_BRANCH" ] || fail "releases are cut from $RELEASE_BRANCH, not '$(git branch --show-current)'"
  fi
  [ -z "$(git status --porcelain --untracked-files=no)" ] || fail "the checkout has uncommitted changes"
  echo "Release request v$VERSION ($MODE) is valid for ${#PUBLISHED_DIRS[@]} published modules."
}

check_manifest() {
  local dir path tracked listed duplicated missing unlisted
  for dir in "${PUBLISHED_DIRS[@]}" "${PRIVATE_DIRS[@]}"; do
    [ -f "$dir/go.mod" ] || fail "manifest lists '$dir', which has no go.mod"
  done
  duplicated=$(printf '%s\n' "${PUBLISHED_DIRS[@]}" "${PRIVATE_DIRS[@]}" | sort | uniq -d)
  [ -z "$duplicated" ] || fail "manifest lists a module twice: $duplicated"
  [ -n "$ROOT_MODULE" ] || fail "manifest must publish the root module '.'"
  # The tag of a nested module is its directory; it only works when the module
  # path ends with that directory.
  for dir in "${PUBLISHED_DIRS[@]}"; do
    path=${PATH_OF[$dir]}
    if [ "$dir" = . ]; then
      continue
    fi
    [ "$path" = "$ROOT_MODULE/$dir" ] || fail "module $dir declares path $path; its tag $dir/v<version> requires $ROOT_MODULE/$dir"
  done
  tracked=$(git ls-files -- go.mod '**/go.mod' | xargs -n1 dirname | sort)
  listed=$(printf '%s\n' "${PUBLISHED_DIRS[@]}" "${PRIVATE_DIRS[@]}" | sort)
  unlisted=$(comm -23 <(echo "$tracked") <(echo "$listed"))
  [ -z "$unlisted" ] || fail "tracked modules missing from .github/modules.txt (add them as published or private): $unlisted"
  missing=$(comm -13 <(echo "$tracked") <(echo "$listed"))
  [ -z "$missing" ] || fail "manifest lists modules that are not tracked in git: $missing"
}

# A version is immutable: any occupied tag, local or remote, blocks the run.
check_tags_unused() {
  local dir tag clashes=()
  git fetch --quiet --tags origin 2>/dev/null || true
  for dir in "${PUBLISHED_DIRS[@]}"; do
    tag=$(tag_for "$dir" "$VERSION")
    if git rev-parse -q --verify "refs/tags/$tag" >/dev/null \
      || git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
      clashes+=("$tag")
    fi
  done
  [ "${#clashes[@]}" -eq 0 ] || fail "tags already exist, refusing to reuse a version: ${clashes[*]}"
}

# ---------------------------------------------------------------------------

# Examples depend on unreleased source, so they always build in the workspace.
build_examples() { # build_examples vet|build
  local pkgs
  mapfile -t pkgs < <(example_packages)
  cd "$REPO_ROOT/examples" || return 1
  if [ "$1" = vet ]; then
    GOWORK="$REPO_ROOT/go.work" "${HERMETIC[@]}" go vet "${pkgs[@]}" || return 1
  fi
  GOWORK="$REPO_ROOT/go.work" "${HERMETIC[@]}" go build -o /dev/null "${pkgs[@]}"
}

# Source checks, shared with ordinary CI. Nested modules still require the
# previous release of their siblings in go.mod, so they run in the workspace
# against current source. The root module has no internal requirements and
# runs alone, which proves the workspace is not hiding a missing dependency.
cmd_test() {
  local dir failed=()
  for dir in "${PUBLISHED_DIRS[@]}"; do
    echo "::group::test $dir"
    if [ "$dir" = . ]; then
      GOWORK=off go_checks "$dir" || failed+=("$dir")
    else
      GOWORK="$REPO_ROOT/go.work" go_checks "$dir" || failed+=("$dir")
    fi
    echo "::endgroup::"
  done
  echo "::group::examples"
  (build_examples vet) || failed+=(examples)
  echo "::endgroup::"
  echo "::group::integration"
  # Live-provider tests skip without MODEL_PROVIDER/MODEL_ID; the rest of the
  # module is compile-checked and its deterministic guards are run.
  (
    cd integration \
      && GOWORK="$REPO_ROOT/go.work" "${HERMETIC[@]}" go vet ./... \
      && GOWORK="$REPO_ROOT/go.work" "${HERMETIC[@]}" go test -count=1 -run '^(TestNo|TestStructured)' ./...
  ) || failed+=(integration)
  echo "::endgroup::"
  [ "${#failed[@]}" -eq 0 ] || fail "failing modules: ${failed[*]}"
}

# ---------------------------------------------------------------------------

# Rewrite requirements between published modules to the release version. Only
# existing edges change, replace directives for published modules are dropped
# (consumers ignore a dependency's replaces), and no source is touched.
cmd_prepare() {
  require_version
  local dir path args requirements replaces
  for dir in "${PUBLISHED_DIRS[@]}"; do
    requirements=$(cd "$dir" && go mod edit -json | jq -r '.Require[]?.Path')
    replaces=$(cd "$dir" && go mod edit -json | jq -r '.Replace[]? | "\(.Old.Path)@\(.Old.Version // "")"')
    args=()
    for path in "${!DIR_OF[@]}"; do
      if grep -qxF "$path" <<< "$requirements"; then
        args+=("-require=$path@v$VERSION")
      fi
      while read -r replace; do
        [ "${replace%@*}" = "$path" ] || continue
        args+=("-dropreplace=${replace%@}")
      done <<< "$replaces"
    done
    if [ "${#args[@]}" -gt 0 ]; then
      (cd "$dir" && go mod edit "${args[@]}")
    fi
  done
  assert_only_module_files_changed
  echo "Prepared ${#PUBLISHED_DIRS[@]} modules at v$VERSION: $(changed_files | wc -l | tr -d ' ') files changed."
}

cmd_seal() {
  require_version
  rm -rf "$PROXY_DIR"
  bash "$SCRIPTS/seal-module-sums.sh" "$VERSION" "$PROXY_DIR"
  assert_only_module_files_changed
}

# The prepared files are checked as they stand: nothing but go.mod and go.sum
# may differ from source, and every module must work on its own.
cmd_verify() {
  require_version
  [ -d "$PROXY_DIR" ] || fail "no sealed proxy at $PROXY_DIR; run 'release.sh seal' first"
  assert_only_module_files_changed
  bash "$SCRIPTS/verify-module-metadata.sh" "$VERSION" "$PROXY_DIR"
  echo "::group::examples"
  (build_examples build)
  echo "::endgroup::"
  assert_only_module_files_changed
}

# ---------------------------------------------------------------------------

commit_message() { echo "chore: release v$VERSION"; }

cmd_plan() {
  require_version
  local dir
  echo "Release version: v$VERSION"
  echo
  echo "Files that would be committed:"
  changed_files | sed 's/^/  /'
  echo
  echo "Commit message:"
  echo "  $(commit_message)"
  echo
  echo "Annotated tags on that commit:"
  for dir in "${PUBLISHED_DIRS[@]}"; do
    echo "  $(tag_for "$dir" "$VERSION")"
  done
}

cmd_commit() {
  require_version
  assert_only_module_files_changed
  local files
  files=$(changed_files)
  [ -n "$files" ] || fail "nothing was prepared; refusing to create an empty release commit"
  git config user.name "github-actions[bot]"
  git config user.email "github-actions[bot]@users.noreply.github.com"
  # shellcheck disable=SC2086
  git add -- $files
  git commit --quiet -m "$(commit_message)"
  [ -z "$(git status --porcelain --untracked-files=all)" ] || fail "the release commit left uncommitted changes"
  echo "Committed $(git rev-parse --short HEAD): $(commit_message)"
}

# Every tag, root included, is an annotated tag on the single release commit.
cmd_tags() {
  require_version
  local dir tag commit
  commit=$(git rev-parse HEAD)
  [ "$(git log -1 --format=%s)" = "$(commit_message)" ] || fail "HEAD is not the release commit '$(commit_message)'"
  for dir in "${PUBLISHED_DIRS[@]}"; do
    tag=$(tag_for "$dir" "$VERSION")
    git tag -a "$tag" -m "Release v$VERSION" "$commit"
    [ "$(git rev-parse "$tag^{commit}")" = "$commit" ] || fail "tag $tag does not point at $commit"
  done
  echo "Created ${#PUBLISHED_DIRS[@]} annotated tags on $(git rev-parse --short "$commit")."
}

# One atomic push: the commit and every tag are published together or not at all.
cmd_push() {
  require_version
  local dir refs=() branch commit
  branch=$(git branch --show-current)
  commit=$(git rev-parse HEAD)
  for dir in "${PUBLISHED_DIRS[@]}"; do
    refs+=("refs/tags/$(tag_for "$dir" "$VERSION")")
  done
  git push --atomic origin "HEAD:refs/heads/$branch" "${refs[@]}"
  for ref in "${refs[@]}"; do
    [ "$(git ls-remote origin "$ref^{}" | awk '{print $1}')" = "$commit" ] || fail "remote $ref does not point at $commit"
  done
  echo "Pushed $branch and ${#refs[@]} tags at $(git rev-parse --short "$commit")."
}

# The tags are already public when this runs, so a transient GitHub API error
# is retried instead of failing a release that otherwise succeeded.
cmd_github_release() {
  require_version
  local attempt
  for attempt in 1 2 3 4; do
    if gh release create "v$VERSION" --verify-tag --title "v$VERSION" --generate-notes; then
      return 0
    fi
    # The request may have created the release before the error was returned.
    if gh release view "v$VERSION" >/dev/null 2>&1; then
      echo "Release v$VERSION exists."
      return 0
    fi
    echo "GitHub release attempt $attempt failed; retrying." >&2
    sleep $((attempt * 10))
  done
  fail "could not create the GitHub release for v$VERSION; the tags are published, so create it by hand with: gh release create v$VERSION --verify-tag --generate-notes"
}

# ---------------------------------------------------------------------------

[ $# -ge 1 ] || fail "usage: release.sh <choose-version|validate|test|prepare|seal|verify|plan|commit|tags|push|github-release> [--version X] [--mode M]"
command=$1
shift
parse_args "$@"
case "$command" in
  choose-version) cmd_choose_version ;;
  github-release) cmd_github_release ;;
  validate | test | prepare | seal | verify | plan | commit | tags | push) "cmd_$command" ;;
  *) fail "unknown command '$command'" ;;
esac
