#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
release_workflow="$repo_root/.github/workflows/release.yaml"
pr_workflow="$repo_root/.github/workflows/pr-test-build.yml"
installer="$repo_root/.github/scripts/install-llvm-mingw.ps1"
config_example="$repo_root/config.example.yaml"
plugin_readme="$repo_root/examples/plugin/vertex-region-models/README.md"

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

assert_contains() {
  local expected="$1"
  local file="$2"
  grep -Fq -- "$expected" "$file" || fail "expected '$expected' in ${file#$repo_root/}"
}

assert_count() {
  local expected_count="$1"
  local expected="$2"
  shift 2
  local actual_count
  actual_count="$(grep -Fhc -- "$expected" "$@" | awk '{ total += $1 } END { print total + 0 }')"
  if [[ "$actual_count" -ne "$expected_count" ]]; then
    fail "expected '$expected' $expected_count times, found $actual_count"
  fi
}

for workflow in "$release_workflow" "$pr_workflow"; do
  assert_contains "LLVM_MINGW_VERSION: '20260616'" "$workflow"
  assert_contains "LLVM_MINGW_SHA256: 'b9b68a4d276e16fa25802aaba458e4638f64b3884c290aaccdc2d87083b6ca35'" "$workflow"
  assert_contains 'runner: windows-latest' "$workflow"
  assert_contains 'cc: x86_64-w64-mingw32-clang' "$workflow"
  assert_contains 'cc: aarch64-w64-mingw32-clang' "$workflow"
  assert_contains '.github/scripts/install-llvm-mingw.ps1' "$workflow"
  assert_contains 'vertex_region_models.dll' "$workflow"
  assert_contains 'mv "$plugin_link_output" "$plugin_output"' "$workflow"
  assert_contains 'plugin_package_dir=' "$workflow"
  assert_contains 'TestDynamicLibraryClientSerializesCalls' "$workflow"
  assert_contains 'TestVertexRegionModelsPluginCABI' "$workflow"
done

if grep -Fq -- 'windows-11-arm' "$release_workflow" "$pr_workflow"; then
  fail 'Windows ARM64 plugin builds must use the pinned cross-toolchain on windows-latest'
fi

assert_count 2 'cc: x86_64-w64-mingw32-clang' "$release_workflow" "$pr_workflow"
assert_count 2 'cc: aarch64-w64-mingw32-clang' "$release_workflow" "$pr_workflow"
assert_count 2 '.github/scripts/install-llvm-mingw.ps1' "$release_workflow" "$pr_workflow"
assert_contains 'bash scripts/tests/windows-plugin-workflow_test.sh' "$pr_workflow"

assert_contains 'plugin_archive_name="vertex-region-models_${RELEASE_VERSION}_windows_${ASSET_ARCH}.zip"' "$release_workflow"
assert_contains 'dist/vertex-region-models_*' "$release_workflow"
assert_count 2 'archives=(CLIProxyAPI_*.tar.gz CLIProxyAPI_*.zip vertex-region-models_*.zip)' "$release_workflow"
assert_contains 'vertex-region-models___VERSION___windows_amd64.zip' "$release_workflow"
assert_contains 'vertex-region-models___VERSION___windows_aarch64.zip' "$release_workflow"
if grep -Fq -- 'plugin_dir="$archive_dir/plugins/' "$release_workflow"; then
  fail 'the main Windows archive must not bundle the Vertex region models plugin'
fi

assert_contains 'dir: "~/.cli-proxy-api/plugins"' "$config_example"
assert_contains '~/.cli-proxy-api/plugins' "$plugin_readme"
if grep -Fq -- 'dir: "@exe/plugins"' "$plugin_readme"; then
  fail 'the Windows plugin instructions must not depend on the WinGet executable alias directory'
fi

assert_contains 'Get-FileHash' "$installer"
assert_contains '$env:GITHUB_PATH' "$installer"
assert_contains 'llvm-mingw-$Version-ucrt-x86_64.zip' "$installer"
assert_contains '${Compiler}.exe' "$installer"
