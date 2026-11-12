#!/usr/bin/env bash
# Shared by the release scripts: module discovery and the per-module checks.
# Source it; it changes no directory and runs nothing.

REPO_ROOT=$(git rev-parse --show-toplevel)

# Modules are discovered from git, so a new go.mod is released (or tested) with
# no list to update. Every tracked go.mod is published and tagged, except the
# private ones: examples is built by CI but never imported by users, and go.mod
# files under testdata belong to test fixtures.
PRIVATE_MODULE_PATTERN='^examples$'
PUBLISHED_DIRS=()
PRIVATE_DIRS=()
while read -r dir; do
  case "$dir" in
    testdata | */testdata | */testdata/*) ;;
    *)
      if [[ $dir =~ $PRIVATE_MODULE_PATTERN ]]; then
        PRIVATE_DIRS+=("$dir")
      else
        PUBLISHED_DIRS+=("$dir")
      fi
      ;;
  esac
done < <(git -C "$REPO_ROOT" ls-files -- go.mod '**/go.mod' | xargs -n1 dirname | sort)

# module directory -> module path, and the reverse, for published modules.
declare -A PATH_OF=() DIR_OF=()
for dir in "${PUBLISHED_DIRS[@]}"; do
  path=$(cd "$REPO_ROOT/$dir" && go mod edit -json | jq -r '.Module.Path')
  PATH_OF["$dir"]=$path
  DIR_OF["$path"]=$dir
done
ROOT_MODULE=${PATH_OF[.]:-}

# Go resolves "<module dir>/vX.Y.Z" as the tag of a module that lives in a
# subdirectory, so every published module needs a tag shaped that way.
tag_for() { # tag_for <dir> <version>
  if [ "$1" = . ]; then echo "v$2"; else echo "$1/v$2"; fi
}

# Commands that must not see credentials or live endpoints, so tests behave the
# same on a laptop and on a runner.
HERMETIC=(env -u POSTGRES_URL -u REDIS_URL -u ANTHROPIC_API_KEY -u OPENAI_API_KEY
  -u AWS_BEARER_TOKEN_BEDROCK -u SENTRY_DSN -u TEST_LIVE_INFERENCE -u TEST_MODEL_ID -u TEST_EMBEDDING_MODEL_ID)

# go_checks <dir>: build, vet and test one module with the caller's Go
# environment (GOWORK, GOFLAGS, GOPROXY). The commands are chained with && so a
# failure counts even when the caller runs this on the left of ||.
go_checks() {
  (
    cd "$REPO_ROOT/$1" \
      && "${HERMETIC[@]}" go build ./... \
      && "${HERMETIC[@]}" go vet ./... \
      && "${HERMETIC[@]}" go test -count=1 ./...
  )
}

# race_checks <dir>: build and run one module's tests under the race detector,
# with the caller's Go environment. The agent runtime is concurrency-heavy
# (streaming iterators, interrupts, a shared Runtime), so the lanes that matter
# run here in addition to the plain go_checks lane. Chained with && so a failure
# counts on the left of ||.
race_checks() {
  (
    cd "$REPO_ROOT/$1" \
      && "${HERMETIC[@]}" go build ./... \
      && "${HERMETIC[@]}" go test -race -count=1 ./...
  )
}

# RACE_DIRS: the subset of published module directories that run under -race in
# CI. The concurrency surface lives in the root module and in provider/*,
# conversation/*, execution/*, ratelimit and tool; running every module under
# -race would roughly double CI time for packages with no shared state. Only
# directories that actually hold a go.mod are kept, so the list survives a module
# being added, moved or removed.
race_dirs() {
  local dir
  for dir in "${PUBLISHED_DIRS[@]}"; do
    case "$dir" in
      . | \
      agent/provider/* | \
      agent/conversation/* | \
      agent/execution | agent/execution/* | \
      agent/ratelimit/* | \
      agent/tool/*)
        echo "$dir" ;;
    esac
  done
}

# example_packages lists the tracked example programs that must compile.
example_packages() {
  git -C "$REPO_ROOT" ls-files -- 'examples/*main.go' \
    | grep -v -e '^examples/agentcore-deploy/' -e '/_local/' \
    | while read -r file; do echo "./$(dirname "${file#examples/}")"; done | sort -u
}
