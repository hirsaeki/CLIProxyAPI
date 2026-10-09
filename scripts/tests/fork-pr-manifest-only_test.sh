#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
classifier="$repo_root/.github/scripts/fork-pr-manifest-only.sh"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
git init -q "$tmp_dir"
cd "$tmp_dir"
git config user.name 'Workflow test'
git config user.email 'workflow-test@example.invalid'
mkdir -p winget internal
for name in hirsaeki.CLIProxyAPI.yaml hirsaeki.CLIProxyAPI.installer.yaml hirsaeki.CLIProxyAPI.locale.en-US.yaml; do
  printf 'PackageVersion: 1.0.0\n' > "winget/$name"
done
printf 'package internal\n' > internal/example.go
git add .
git commit -qm base
base="$(git rev-parse HEAD)"

assert_result() {
  local expected="$1" actual
  actual="$(bash "$classifier" "$base" "$(git rev-parse HEAD)")"
  [[ "$actual" == "$expected" ]] || { echo "expected $expected, got $actual" >&2; exit 1; }
}
commit_case() { git add -A; git commit -qm test; }
reset_case() { git reset --hard -q "$base"; git clean -fdq; }

assert_result false # Empty diffs must not bypass builds.
for name in hirsaeki.CLIProxyAPI.yaml hirsaeki.CLIProxyAPI.installer.yaml hirsaeki.CLIProxyAPI.locale.en-US.yaml; do
  reset_case
  printf 'PackageVersion: 1.0.1\n' > "winget/$name"
  commit_case
  assert_result true
done
reset_case
sed -i 's/1.0.0/1.0.1/' winget/*.yaml
commit_case
assert_result true
printf '// code change\n' >> internal/example.go
commit_case
assert_result false # Mixed source/manifest changes always build.

for path in README.md .github/workflows/pr-test-build.yml scripts/test.sh winget/other.yaml $'winget/unexpected\nfile.yaml'; do
  reset_case
  mkdir -p "$(dirname "$path")"
  printf 'unexpected\n' > "$path"
  commit_case
  assert_result false
done
reset_case
git mv internal/example.go winget/hirsaeki.CLIProxyAPI.yaml.tmp
mv winget/hirsaeki.CLIProxyAPI.yaml.tmp winget/hirsaeki.CLIProxyAPI.yaml
commit_case
assert_result false # Renaming source into an allowed path cannot hide deletion.
reset_case
git mv winget/hirsaeki.CLIProxyAPI.yaml README.md
commit_case
assert_result false

if bash "$classifier" invalid "$base" >/dev/null 2>&1; then
  echo 'invalid commit input accepted' >&2; exit 1
fi
if bash "$classifier" "$base" 0000000000000000000000000000000000000000 >/dev/null 2>&1; then
  echo 'missing commit accepted' >&2; exit 1
fi
echo 'Manifest-only PR classification checks passed'
