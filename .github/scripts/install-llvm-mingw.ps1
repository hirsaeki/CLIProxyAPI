[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^\d{8}$')]
    [string]$Version,

    [Parameter(Mandatory = $true)]
    [ValidatePattern('^[0-9a-fA-F]{64}$')]
    [string]$Sha256,

    [Parameter(Mandatory = $true)]
    [ValidateSet('x86_64-w64-mingw32-clang', 'aarch64-w64-mingw32-clang')]
    [string]$Compiler
)

$ErrorActionPreference = 'Stop'

$archiveName = "llvm-mingw-$Version-ucrt-x86_64.zip"
$archivePath = Join-Path $env:RUNNER_TEMP $archiveName
$destination = Join-Path $env:RUNNER_TEMP "llvm-mingw-$Version-ucrt-x86_64"
$downloadUrl = "https://github.com/mstorsjo/llvm-mingw/releases/download/$Version/$archiveName"

Invoke-WebRequest -Uri $downloadUrl -OutFile $archivePath

$actualSha256 = (Get-FileHash -Path $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualSha256 -ne $Sha256.ToLowerInvariant()) {
    throw "LLVM-MinGW checksum mismatch: expected $Sha256, got $actualSha256"
}

if (Test-Path $destination) {
    Remove-Item -Path $destination -Recurse -Force
}
Expand-Archive -Path $archivePath -DestinationPath $destination

$compilerPath = Get-ChildItem -Path $destination -Recurse -File -Filter "${Compiler}.exe" |
    Select-Object -First 1
if ($null -eq $compilerPath) {
    throw "LLVM-MinGW compiler not found after extraction: $Compiler"
}

Add-Content -Path $env:GITHUB_PATH -Value $compilerPath.DirectoryName -Encoding utf8
