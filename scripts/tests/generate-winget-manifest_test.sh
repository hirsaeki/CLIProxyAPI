#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

checksums_file="$tmp_dir/checksums.txt"
manifest_dir="$tmp_dir/winget"

cat > "$checksums_file" <<'EOF'
aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa *CLIProxyAPI_7.2.60_windows_amd64.zip
bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  CLIProxyAPI_7.2.60_windows_aarch64.zip
EOF

"$repo_root/scripts/generate-winget-manifest.sh" \
  v7.2.60 \
  hirsaeki/CLIProxyAPI \
  "$checksums_file" \
  "$manifest_dir"

assert_contains() {
  local expected="$1"
  local manifest_file="$2"
  if ! grep -Fqx -- "$expected" "$manifest_file"; then
    printf 'expected manifest line not found: %s\n' "$expected" >&2
    exit 1
  fi
}

version_manifest="$manifest_dir/hirsaeki.CLIProxyAPI.yaml"
installer_manifest="$manifest_dir/hirsaeki.CLIProxyAPI.installer.yaml"
locale_manifest="$manifest_dir/hirsaeki.CLIProxyAPI.locale.en-US.yaml"

manifest_count="$(find "$manifest_dir" -maxdepth 1 -type f -name '*.yaml' | wc -l)"
if [[ "$manifest_count" -ne 3 ]]; then
  printf 'expected three manifest files, found %s\n' "$manifest_count" >&2
  exit 1
fi

assert_contains 'PackageVersion: 7.2.60' "$version_manifest"
assert_contains 'ManifestType: version' "$version_manifest"
assert_contains 'PackageUrl: https://github.com/hirsaeki/CLIProxyAPI' "$locale_manifest"
assert_contains '  InstallerUrl: https://github.com/hirsaeki/CLIProxyAPI/releases/download/v7.2.60/CLIProxyAPI_7.2.60_windows_amd64.zip' "$installer_manifest"
assert_contains '  InstallerSha256: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"' "$installer_manifest"
assert_contains '  InstallerUrl: https://github.com/hirsaeki/CLIProxyAPI/releases/download/v7.2.60/CLIProxyAPI_7.2.60_windows_aarch64.zip' "$installer_manifest"
assert_contains '  InstallerSha256: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"' "$installer_manifest"

: > "$tmp_dir/empty-checksums.txt"
if "$repo_root/scripts/generate-winget-manifest.sh" \
  v7.2.60 \
  hirsaeki/CLIProxyAPI \
  "$tmp_dir/empty-checksums.txt" \
  "$tmp_dir/invalid" 2>/dev/null; then
  echo 'generator unexpectedly accepted missing release checksums' >&2
  exit 1
fi
