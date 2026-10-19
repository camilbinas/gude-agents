#!/usr/bin/env bash
# Shared by the release scripts: the module manifest and the per-module checks.
# Source it; it changes no directory and runs nothing.

REPO_ROOT=$(git rev-parse --show-toplevel)
MANIFEST="$REPO_ROOT/.github/modules.txt"

# Published modules are released together; private ones are only built in CI.
PUBLISHED_DIRS=()
PRIVATE_DIRS=()
while read -r kind dir _; do
  case "$kind" in
    published) PUBLISHED_DIRS+=("$dir") ;;
    private) PRIVATE_DIRS+=("$dir") ;;
    '' | '#'*) ;;
    *) echo "::error::$MANIFEST: unknown entry '$kind $dir'" >&2; exit 1 ;;
  esac
done < "$MANIFEST"

# module directory -> module path, and the reverse, for published modules.
declare -A PATH_OF=() DIR_OF=()
for dir in "${PUBLISHED_DIRS[@]}"; do
  [ -f "$REPO_ROOT/$dir/go.mod" ] || continue
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
  -u AWS_BEARER_TOKEN_BEDROCK -u SENTRY_DSN -u MODEL_PROVIDER -u MODEL_ID -u GUDE_LIVE_TESTS)

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

# example_packages lists the tracked example programs that must compile.
example_packages() {
  git -C "$REPO_ROOT" ls-files -- 'examples/*main.go' \
    | grep -v -e '^examples/agentcore-deploy/' -e '/_local/' \
    | while read -r file; do echo "./$(dirname "${file#examples/}")"; done | sort -u
}
