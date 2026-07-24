#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
release_workflow="$repo_root/.github/workflows/release.yaml"
pr_workflow="$repo_root/.github/workflows/pr-test-build.yml"
quick_start="$repo_root/docs/oauth-model-availability.md"

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

assert_contains() {
  local expected="$1"
  local file="$2"
  grep -Fq -- "$expected" "$file" || fail "expected '$expected' in ${file#$repo_root/}"
}

assert_min_count() {
  local minimum="$1"
  local expected="$2"
  local file="$3"
  local actual
  actual="$(grep -Fc -- "$expected" "$file" || true)"
  if [[ "$actual" -lt "$minimum" ]]; then
    fail "expected '$expected' at least $minimum times in ${file#$repo_root/}, found $actual"
  fi
}

assert_min_count 4 './cmd/sync_oauth_model_availability/' "$release_workflow"
assert_contains 'sync-oauth-model-availability.exe' "$release_workflow"
assert_contains 'docs/oauth-model-availability.md' "$release_workflow"
assert_contains 'sync-oauth-model-availability' "$pr_workflow"
assert_contains 'bash scripts/tests/oauth-model-availability-release_test.sh' "$pr_workflow"
assert_contains 'sync-oauth-model-availability' "$quick_start"
