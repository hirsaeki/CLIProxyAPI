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

WinGet selects the x64 or ARM64 server archive for the current machine. The
archive contains `cli-proxy-api.exe` and documentation, but no plugin DLL.

### Install the Vertex region models plugin

Each GitHub Release links separate x64 and ARM64 plugin ZIPs. The ZIPs preserve
the `plugins/windows/<go-arch>` directory tree (`amd64` or `arm64`) and are
intended to be extracted into the user's `.cli-proxy-api` directory. Asset names
use `amd64` for x64 and `aarch64` for ARM64. For example, set the release tag and
run the following from PowerShell:

```powershell
$releaseTag = Read-Host "Release tag (for example, v7.2.92.2)"
$releaseVersion = $releaseTag.Substring(1)
$assetArch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq "Arm64") {
  "aarch64"
} else {
  "amd64"
}
$assetName = "vertex-region-models_${releaseVersion}_windows_${assetArch}.zip"
$pluginArchive = Join-Path $env:TEMP $assetName
$pluginHome = Join-Path $HOME ".cli-proxy-api"

Invoke-WebRequest `
  -Uri "https://github.com/hirsaeki/CLIProxyAPI/releases/download/$releaseTag/$assetName" `
  -OutFile $pluginArchive
Expand-Archive -Path $pluginArchive -DestinationPath $pluginHome -Force
```

The plugin is trusted in-process code and remains opt-in. Add the following to
the runtime configuration:

```yaml
plugins:
  enabled: true
  dir: "~/.cli-proxy-api/plugins"
  configs:
    vertex-region-models:
      enabled: true
      priority: 20
      fail_open: false
```

The host expands `~` to the current user's profile directory. Do not use
`@exe/plugins` for this WinGet installation: when the command is launched
through `Microsoft\WinGet\Links`, executable-relative discovery can resolve
against the alias directory instead of the package directory. WinGet does not
create or modify `config.yaml` during install or upgrade. A stable invocation
can keep the file at `~/.cli-proxy-api/config.yaml` and pass it explicitly:

```powershell
cli-proxy-api --config "$HOME\.cli-proxy-api\config.yaml"
```

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

The plugin is a separate Release asset and is not updated by WinGet. Download
and extract the matching plugin ZIP again when moving to a newer plugin build.

Uninstall the registered portable package by its identifier:

```powershell
winget uninstall --id hirsaeki.CLIProxyAPI --exact
```

Commands such as `winget search hirsaeki.CLIProxyAPI` and
`winget install hirsaeki.CLIProxyAPI` do not work because this package is not
published to a configured WinGet source.

## Release automation

The `release` GitHub Actions workflow publishes server archives and separate
Windows plugin ZIPs, then updates the manifest after the final checksum file has
been published:

1. The build publishes x64 and ARM64 server ZIPs plus matching Vertex plugin
   ZIPs. Release notes link directly to both plugin assets.
2. The WinGet job downloads only the Windows x64 and ARM64 server ZIPs from the
   GitHub Release.
3. It computes SHA256 checksums and runs
   `scripts/generate-winget-manifest.sh`.
4. A Windows runner validates the generated version, installer, and default
   locale manifests with `winget validate`.
5. `peter-evans/create-pull-request` creates or updates
   `automation/winget-v<version>` against the default branch.
6. A maintainer reviews and merges the pull request. It is not automatically
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

The manifest pull request is not created when either Windows server archive is
missing, a checksum is invalid, manifest generation fails, or `winget validate`
rejects the result. Plugin packaging failures fail the corresponding Windows
build before final checksums and manifest automation. These failures do not
delete or roll back release assets that were already published.

Inspect the `update WinGet manifest` job in the release workflow first. After
fixing the cause, use **Re-run failed jobs** on the same workflow run. Re-running
the job for the same tag updates the existing automation branch and pull request
instead of creating a duplicate.

Before merging the generated pull request, verify:

- `PackageVersion` matches the release tag without the leading `v`.
- The x64 and ARM64 installer URLs point to the same GitHub Release.
- Both SHA256 values match the corresponding ZIP assets.
- The server ZIPs do not contain plugin DLLs.
- Each plugin ZIP contains
  `plugins/windows/<arch>/vertex-region-models-v<version>.dll`.
- `NestedInstallerFiles` contains only `cli-proxy-api.exe`.
- The `winget validate` step completed successfully.
