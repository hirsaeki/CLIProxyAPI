#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 <release-tag> <owner/repository> <checksums-file> <output-directory>" >&2
  exit 2
fi

release_tag="$1"
repository="$2"
checksums_file="$3"
output_dir="$4"

if [[ ! "$release_tag" =~ ^v[0-9]+([.][0-9]+)+$ ]]; then
  echo "release tag must use the form v<numeric-version>: $release_tag" >&2
  exit 2
fi
if [[ ! "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "repository must use the form owner/name: $repository" >&2
  exit 2
fi
if [[ ! -f "$checksums_file" ]]; then
  echo "checksums file not found: $checksums_file" >&2
  exit 2
fi

package_version="${release_tag#v}"
amd64_asset="CLIProxyAPI_${package_version}_windows_amd64.zip"
arm64_asset="CLIProxyAPI_${package_version}_windows_aarch64.zip"

checksum_for() {
  local asset_name="$1"
  local checksum
  checksum="$(awk -v name="$asset_name" '$2 == name { print toupper($1) }' "$checksums_file")"
  if [[ ! "$checksum" =~ ^[A-F0-9]{64}$ ]]; then
    echo "missing or invalid SHA256 checksum for $asset_name" >&2
    exit 1
  fi
  printf '%s' "$checksum"
}

amd64_checksum="$(checksum_for "$amd64_asset")"
arm64_checksum="$(checksum_for "$arm64_asset")"

mkdir -p "$output_dir"
cat > "$output_dir/hirsaeki.CLIProxyAPI.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.version.1.10.0.schema.json

PackageIdentifier: hirsaeki.CLIProxyAPI
PackageVersion: $package_version
DefaultLocale: en-US
ManifestType: version
ManifestVersion: 1.10.0
EOF

cat > "$output_dir/hirsaeki.CLIProxyAPI.installer.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.installer.1.10.0.schema.json

PackageIdentifier: hirsaeki.CLIProxyAPI
PackageVersion: $package_version
InstallerType: zip
NestedInstallerType: portable
UpgradeBehavior: install
Installers:
- Architecture: x64
  NestedInstallerFiles:
  - RelativeFilePath: cli-proxy-api.exe
    PortableCommandAlias: cli-proxy-api
  InstallerUrl: https://github.com/$repository/releases/download/$release_tag/$amd64_asset
  InstallerSha256: "$amd64_checksum"
- Architecture: arm64
  NestedInstallerFiles:
  - RelativeFilePath: cli-proxy-api.exe
    PortableCommandAlias: cli-proxy-api
  InstallerUrl: https://github.com/$repository/releases/download/$release_tag/$arm64_asset
  InstallerSha256: "$arm64_checksum"
ManifestType: installer
ManifestVersion: 1.10.0
EOF

cat > "$output_dir/hirsaeki.CLIProxyAPI.locale.en-US.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.defaultLocale.1.10.0.schema.json

PackageIdentifier: hirsaeki.CLIProxyAPI
PackageVersion: $package_version
PackageLocale: en-US
Publisher: Router-For.ME
PublisherUrl: https://github.com/router-for-me
PackageName: CLIProxyAPI
PackageUrl: https://github.com/$repository
License: MIT
LicenseUrl: https://github.com/$repository/blob/main/LICENSE
ShortDescription: OpenAI, Gemini, Claude, Codex, and Grok compatible API proxy server
ManifestType: defaultLocale
ManifestVersion: 1.10.0
EOF
