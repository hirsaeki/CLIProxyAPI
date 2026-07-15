# WinGet installation and release automation

This fork publishes repository-local WinGet manifests for the Windows x64 and
ARM64 release archives. These manifests are not submitted to the Microsoft
Community Package Repository and do not define a custom WinGet REST source. As
a result, install and upgrade commands must explicitly reference a downloaded
manifest directory.

## Install on Windows

Download the three latest manifest files from this repository, optionally
validate them, and then install the package:

Local manifest installation is disabled by default. Enable it once from an
elevated PowerShell session, and only install manifests from repositories you
trust:

```powershell
winget settings --enable LocalManifestFiles
```

Then download and use the manifests from a normal PowerShell session:

```powershell
$manifestDir = Join-Path $env:TEMP "CLIProxyAPI-winget"
New-Item -ItemType Directory -Path $manifestDir -Force | Out-Null

$manifestFiles = @(
  "hirsaeki.CLIProxyAPI.yaml",
  "hirsaeki.CLIProxyAPI.installer.yaml",
  "hirsaeki.CLIProxyAPI.locale.en-US.yaml"
)

foreach ($file in $manifestFiles) {
  Invoke-WebRequest `
    -Uri "https://raw.githubusercontent.com/hirsaeki/CLIProxyAPI/main/winget/$file" `
    -OutFile (Join-Path $manifestDir $file)
}

winget validate --manifest $manifestDir
winget install --manifest $manifestDir
cli-proxy-api --version
```

WinGet selects the x64 or ARM64 release archive for the current machine. The
archive contains `cli-proxy-api.exe`, which is installed as a portable package
and exposed through the `cli-proxy-api` command alias.

The manifests are created by the first successful release after this automation
is merged. Until its automated pull request is merged, the raw manifest URLs
above will not exist or will still describe the previous release.

## Upgrade or uninstall

Repository-local manifests are not searchable by package identifier and are not
included in a normal `winget upgrade --all`. Download the current manifest again
before upgrading:

```powershell
$manifestDir = Join-Path $env:TEMP "CLIProxyAPI-winget"
New-Item -ItemType Directory -Path $manifestDir -Force | Out-Null

$manifestFiles = @(
  "hirsaeki.CLIProxyAPI.yaml",
  "hirsaeki.CLIProxyAPI.installer.yaml",
  "hirsaeki.CLIProxyAPI.locale.en-US.yaml"
)

foreach ($file in $manifestFiles) {
  Invoke-WebRequest `
    -Uri "https://raw.githubusercontent.com/hirsaeki/CLIProxyAPI/main/winget/$file" `
    -OutFile (Join-Path $manifestDir $file)
}

winget upgrade --manifest $manifestDir
```

Uninstall the registered portable package by its identifier:

```powershell
winget uninstall --id hirsaeki.CLIProxyAPI --exact
```

Commands such as `winget search hirsaeki.CLIProxyAPI` and
`winget install hirsaeki.CLIProxyAPI` do not work because this package is not
published to a configured WinGet source.

## Release automation

The `release` GitHub Actions workflow updates the manifest after all release
archives have been built and the final checksum file has been published:

1. The workflow downloads the Windows x64 and ARM64 ZIP assets from the GitHub
   Release.
2. It computes SHA256 checksums and runs
   `scripts/generate-winget-manifest.sh`.
3. A Windows runner validates the generated version, installer, and default
   locale manifests with `winget validate`.
4. `peter-evans/create-pull-request` creates or updates
   `automation/winget-v<version>` against the default branch.
5. A maintainer reviews and merges the pull request. It is not automatically
   merged.

The generated manifests are deterministic for a release tag, repository, and
checksum file. Their generator is covered by
`scripts/tests/generate-winget-manifest_test.sh`, which also runs in the pull
request workflow dedicated to WinGet manifest changes.

## Repository settings and permissions

The manifest job grants only the permissions it needs:

- `contents: write` to push the automation branch
- `pull-requests: write` to create or update the pull request

In the repository settings, enable **Allow GitHub Actions to create and approve
pull requests** under **Actions > General > Workflow permissions**. The workflow
creates pull requests but does not approve them.

## Failure handling

The manifest pull request is not created when either Windows archive is missing,
a checksum is invalid, manifest generation fails, or `winget validate` rejects
the result. These failures do not delete or roll back release assets that were
already published.

Inspect the `update WinGet manifest` job in the release workflow first. After
fixing the cause, use **Re-run failed jobs** on the same workflow run. Re-running
the job for the same tag updates the existing automation branch and pull request
instead of creating a duplicate.

Before merging the generated pull request, verify:

- `PackageVersion` matches the release tag without the leading `v`.
- The x64 and ARM64 installer URLs point to the same GitHub Release.
- Both SHA256 values match the corresponding ZIP assets.
- The `winget validate` step completed successfully.
