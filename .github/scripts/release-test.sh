#!/usr/bin/env bash
# Checks release.sh against throwaway repositories: version ordering, the module
# module discovery, automatic version choice, and the atomic push when master moves
# after the version was chosen. No network and no module downloads.
set -uo pipefail
export LC_ALL=C
SCRIPTS=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0

g() { git -c user.email=t@example.com -c user.name=test "$@"; }
release() { bash .github/scripts/release.sh "$@"; }

# fixture <name>: a repo with a root module and one nested module and an
# "origin" it has pushed master to. Modules are discovered from git.
fixture() {
  local dir="$work/$1"
  rm -rf "$dir" "$dir-origin.git"
  mkdir -p "$dir/.github" "$dir/sub"
  cp -r "$SCRIPTS" "$dir/.github/scripts"
  rm -f "$dir/.github/scripts/release-test.sh"
  printf 'module example.com/x\n\ngo 1.25.0\n' > "$dir/go.mod"
  printf 'module example.com/x/sub\n\ngo 1.25.0\n\nrequire example.com/x v0.0.1\n' > "$dir/sub/go.mod"
  git init -q -b master "$dir" && cd "$dir" || exit 1
  g add -A && g commit -qm "feat: start"
  git init -q --bare "$dir-origin.git"
  git remote add origin "$dir-origin.git" && git push -q origin master
}

pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; failures=$((failures + 1)); }
expect_ok() { local name=$1; shift; if "$@" >/dev/null 2>&1; then pass "$name"; else fail "$name (command failed: $*)"; fi; }
expect_fail() { local name=$1; shift; if "$@" >/dev/null 2>&1; then fail "$name (expected failure: $*)"; else pass "$name"; fi; }
expect_message() { # expect_message <name> <pattern> <command...>
  local name=$1 pattern=$2 out; shift 2
  out=$("$@" 2>&1) && { fail "$name (expected failure)"; return; }
  if grep -q -- "$pattern" <<< "$out"; then pass "$name"; else fail "$name (message lacked '$pattern': $out)"; fi
}

# --- Manual versions must be newer -----------------------------------------
fixture ordering
git tag v1.8.8 && git tag v1.9.0-rc.2 && git tag v1.9.0-rc.10
expect_ok      "newer patch is accepted"                    release validate --version 1.8.9
expect_ok      "newer minor is accepted"                    release validate --version 1.10.0
expect_message "same version is rejected"                   "already exist\|not newer" release validate --version 1.8.8
expect_message "older version is rejected"                  "not newer than the latest release v1.8.8" release validate --version 1.8.7
expect_message "prerelease of an older version is rejected" "not newer than the latest release v1.8.8" release validate --version 1.8.8-rc.1
expect_ok      "prerelease of a newer version is accepted"  release validate --version 1.8.9-rc.1
expect_message "older prerelease number is rejected"        "not newer than the existing prerelease" release validate --version 1.9.0-rc.1
expect_message "prerelease compares numerically (rc.9 < rc.10)" "not newer than the existing prerelease" release validate --version 1.9.0-rc.9
expect_ok      "next prerelease is accepted"                release validate --version 1.9.0-rc.11
expect_ok      "final after its prerelease is accepted"     release validate --version 1.9.0
expect_ok      "patch while a newer line is in rc is accepted" release validate --version 1.8.9
expect_message "a v prefix is rejected"                     "must not start with 'v'" release validate --version v1.9.0
expect_message "an existing nested-module tag is rejected"  "already exist" bash -c 'git tag sub/v2.0.0-x >/dev/null; git tag sub/v1.8.9; bash .github/scripts/release.sh validate --version 1.8.9'
git tag -d sub/v1.8.9 sub/v2.0.0-x >/dev/null

# --- Modules are discovered, not listed -------------------------------------
fixture modules
published() { release modules 2>/dev/null | sed -n 's/^published //p' | tr '\n' ' '; }
[ "$(published)" = ". sub " ] && pass "every tracked go.mod is published" || fail "every tracked go.mod is published ($(published))"
mkdir other && printf 'module example.com/x/other\n\ngo 1.25.0\n' > other/go.mod && g add -A && g commit -qm "feat: add other module"
[ "$(published)" = ". other sub " ] && pass "a new go.mod is picked up with no list to edit" || fail "a new go.mod is picked up ($(published))"
expect_ok "the new module gets a nested tag in the plan" bash -c 'bash .github/scripts/release.sh plan --version 1.0.0 | grep -qx "  other/v1.0.0"'
mkdir -p examples integration sub/testdata/fixture
for d in examples integration sub/testdata/fixture; do printf 'module example.com/x/%s\n\ngo 1.25.0\n' "$d" > "$d/go.mod"; done
g add -A && g commit -qm "test: private modules"
[ "$(published)" = ". other sub " ] && pass "examples, integration and testdata modules are never published" || fail "private modules were published ($(published))"
printf 'module example.com/elsewhere\n\ngo 1.25.0\n' > other/go.mod && g add -A && g commit -qm "feat: wrong path"
expect_message "a module path that does not match its directory is reported" "other/v<version> requires" release modules

# --- Automatic version choice -----------------------------------------------
fixture choose
g tag v1.2.3 HEAD
choose() { GITHUB_ENV="$work/env" bash .github/scripts/release.sh choose-version "$@" >/dev/null 2>&1; tail -2 "$work/env" | tr '\n' ' '; : > "$work/env"; }
: > "$work/env"
[ "$(choose)" = "RELEASE_VERSION= RELEASE_MODE=skip " ] && pass "no commits since the tag skips" || fail "no commits since the tag skips"
g commit -q --allow-empty -m "docs: words"
[ "$(choose)" = "RELEASE_VERSION= RELEASE_MODE=skip " ] && pass "docs only skips" || fail "docs only skips"
g commit -q --allow-empty -m "fix: bug"
[ "$(choose)" = "RELEASE_VERSION=1.2.4 RELEASE_MODE=release " ] && pass "fix bumps the patch" || fail "fix bumps the patch"
g commit -q --allow-empty -m "feat: thing"
[ "$(choose)" = "RELEASE_VERSION=1.3.0 RELEASE_MODE=release " ] && pass "feat bumps the minor" || fail "feat bumps the minor"
g commit -q --allow-empty -m "refactor!: break"
[ "$(choose)" = "RELEASE_VERSION= RELEASE_MODE=skip " ] && pass "a breaking change is never automatic" || fail "a breaking change is never automatic"
[ "$(choose --version 1.9.0 --mode dry-run)" = "RELEASE_VERSION=1.9.0 RELEASE_MODE=dry-run " ] && pass "a manual dry run keeps its mode" || fail "a manual dry run keeps its mode"
expect_fail "a manual run with a bad mode is rejected" release choose-version --version 1.9.0 --mode maybe

# --- Master moves after the version was chosen ------------------------------
fixture race
g tag v1.2.3 && g push -q origin v1.2.3
g commit -q --allow-empty -m "fix: bug"
env_file="$work/race-env"; : > "$env_file"
GITHUB_ENV="$env_file" release choose-version >/dev/null 2>&1
version=$(sed -n 's/^RELEASE_VERSION=//p' "$env_file")
[ "$version" = 1.2.4 ] && pass "the version is chosen from the checkout" || fail "the version is chosen from the checkout ($version)"
release prepare --version "$version" >/dev/null 2>&1 && release commit --version "$version" >/dev/null 2>&1 && release tags --version "$version" >/dev/null 2>&1 \
  && pass "the release is committed and tagged locally" || fail "the release is committed and tagged locally"
# A commit lands on master after the version was chosen.
git clone -q "$work/race-origin.git" "$work/race-other" && (cd "$work/race-other" && g commit -q --allow-empty -m "fix: late arrival" && git push -q origin master)
remote_tags_before=$(git -C "$work/race-origin.git" tag | wc -l | tr -d ' ')
expect_fail "the atomic push is rejected" release push --version "$version"
remote_tags_after=$(git -C "$work/race-origin.git" tag | wc -l | tr -d ' ')
[ "$remote_tags_before" = "$remote_tags_after" ] && pass "a rejected push publishes no tags" || fail "a rejected push published tags"
[ "$(git -C "$work/race-origin.git" log -1 --format=%s master)" = "fix: late arrival" ] && pass "master keeps the commit that arrived late" || fail "master was overwritten"

# --- A clean release publishes the commit and every tag together ------------
fixture publish
g tag v1.2.3 && g push -q origin v1.2.3
g commit -q --allow-empty -m "fix: bug"
release prepare --version 1.2.4 >/dev/null 2>&1 && release commit --version 1.2.4 >/dev/null 2>&1 && release tags --version 1.2.4 >/dev/null 2>&1
expect_ok "an unmoved master accepts the push" release push --version 1.2.4
[ "$(git -C "$work/publish-origin.git" tag --list '*1.2.4' | wc -l | tr -d ' ')" = 2 ] && pass "the root and nested tags are both published" || fail "tags missing on the remote"
expect_message "a published version cannot be reused" "already exist" release validate --version 1.2.4

echo
if [ "$failures" -gt 0 ]; then
  echo "$failures release check(s) failed."
  exit 1
fi
echo "All release checks passed."
