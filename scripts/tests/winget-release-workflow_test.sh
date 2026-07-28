#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
release_workflow="$repo_root/.github/workflows/release.yaml"
test_workflow="$repo_root/.github/workflows/winget-manifest-test.yml"

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

assert_contains() {
  local expected="$1"
  local file="$2"
  grep -Fq -- "$expected" "$file" || fail "expected '$expected' in ${file#$repo_root/}"
}

assert_contains 'id: create-winget-pr' "$release_workflow"
assert_contains 'token: ${{ secrets.UPSTREAM_SYNC_TOKEN }}' "$release_workflow"
assert_contains 'name: Merge WinGet manifest after checks pass' "$release_workflow"
assert_contains "if: steps.create-winget-pr.outputs.pull-request-number != ''" "$release_workflow"
assert_contains 'timeout-minutes: 120' "$release_workflow"
assert_contains 'GH_TOKEN: ${{ github.token }}' "$release_workflow"
assert_contains 'PR_NUMBER: ${{ steps.create-winget-pr.outputs.pull-request-number }}' "$release_workflow"
assert_contains 'select(.conclusion == "action_required")' "$release_workflow"
assert_contains 'Timed out waiting for required PR checks to register.' "$release_workflow"
assert_contains 'build Windows plugin (amd64)' "$release_workflow"
assert_contains 'build Windows plugin (arm64)' "$release_workflow"
assert_contains 'gh pr checks "$PR_NUMBER" --watch --fail-fast' "$release_workflow"
assert_contains 'gh pr merge --merge --delete-branch "$PR_NUMBER"' "$release_workflow"
assert_contains 'bash scripts/tests/winget-release-workflow_test.sh' "$test_workflow"
