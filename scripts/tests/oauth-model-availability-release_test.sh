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

assert_not_contains() {
  local unexpected="$1"
  local file="$2"
  if grep -Fq -- "$unexpected" "$file"; then
    fail "did not expect '$unexpected' in ${file#$repo_root/}"
  fi
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

assert_min_count 3 './cmd/sync_oauth_model_availability/' "$release_workflow"
assert_min_count 3 'helper_archive_name="oauth-model-availability-helper_${RELEASE_VERSION}_' "$release_workflow"
assert_contains 'sync-oauth-model-availability.exe' "$release_workflow"
assert_contains 'dist/oauth-model-availability-helper_*' "$release_workflow"
assert_contains 'docs/oauth-model-availability.md' "$release_workflow"
assert_contains 'sync-oauth-model-availability' "$pr_workflow"
assert_contains 'bash scripts/tests/oauth-model-availability-release_test.sh' "$pr_workflow"
assert_contains 'sync-oauth-model-availability' "$quick_start"
assert_contains 'oauth-model-availability-helper_' "$quick_start"
assert_not_contains 'PortableCommandAlias: sync-oauth-model-availability' "$repo_root/scripts/generate-winget-manifest.sh"
assert_not_contains '-o "$archive_dir/$helper_name"' "$release_workflow"

if grep -Fq 'cp docs/oauth-model-availability.md "$archive_dir/' "$release_workflow"; then
  fail 'main release archives must not contain the OAuth model availability documentation'
fi
if grep -F 'tar -C "$archive_dir" -czf "dist/$archive_name"' "$release_workflow" | grep -Fq 'docs/oauth-model-availability.md'; then
  fail 'main release archives must not contain the OAuth model availability documentation'
fi

if grep -F 'tar -C "$archive_dir" -czf "dist/$archive_name"' "$release_workflow" | grep -Fq 'sync-oauth-model-availability'; then
  fail 'main release archives must not contain the OAuth model availability helper'
fi
