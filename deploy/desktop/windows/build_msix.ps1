# Build a BiuMind Windows MSIX package.
#
# Must run on Windows with the Flutter Windows toolchain installed
# (Visual Studio 2022 + "Desktop development with C++" workload).
#
# Optional inputs (env vars):
#   BIUMIND_PUBLISHER       — full publisher DN, e.g.
#                             'CN=BiuMind Inc., O=BiuMind Inc., C=US'
#                             Defaults to a self-signed test value.
#   BIUMIND_CERT_PATH       — path to a .pfx codesigning cert; if set the
#                             MSIX is signed automatically.
#   BIUMIND_CERT_PASSWORD   — password for the .pfx.
#
# Output:
#   build/windows/biumind-<version>-<arch>.msix
#
# Pubspec is expected to declare `msix` under dev_dependencies and a
# `msix_config` block — see deploy/desktop/windows/README.md.

[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

Push-Location (Join-Path $PSScriptRoot '..\..\..')
try {
  $clientDir = 'apps\client'
  if (-not (Test-Path $clientDir)) { throw "missing $clientDir" }

  $version = (Get-Content "$clientDir\pubspec.yaml" |
              Select-String '^version:\s+(.+)$').Matches[0].Groups[1].Value
  $version = $version -replace '\+.*$', ''
  $arch = $env:PROCESSOR_ARCHITECTURE.ToLower()
  $outDir = 'build\windows'
  New-Item -ItemType Directory -Force -Path $outDir | Out-Null

  Write-Host "[msix] building Flutter windows release ($version-$arch)" -ForegroundColor Cyan
  Push-Location $clientDir
  try {
    flutter build windows --release
    if ($LASTEXITCODE -ne 0) { throw 'flutter build failed' }

    $publisher = if ($env:BIUMIND_PUBLISHER) {
      $env:BIUMIND_PUBLISHER
    } else {
      'CN=BiuMind Test, O=BiuMind, C=US'
    }

    $msixArgs = @(
      'pub','run','msix:create',
      '--display-name','BiuMind',
      '--publisher-display-name','BiuMind',
      '--publisher', $publisher,
      '--identity-name','app.biu.biumind',
      '--version',"$version.0",
      '--architecture', $arch
    )
    if ($env:BIUMIND_CERT_PATH) {
      $msixArgs += @(
        '--certificate-path', $env:BIUMIND_CERT_PATH,
        '--certificate-password', $env:BIUMIND_CERT_PASSWORD
      )
    }
    Write-Host '[msix] running msix:create' -ForegroundColor Cyan
    & dart @msixArgs
    if ($LASTEXITCODE -ne 0) { throw 'msix:create failed' }
  } finally {
    Pop-Location
  }

  $built = Get-ChildItem "$clientDir\build\windows" -Recurse -Filter '*.msix' |
           Sort-Object LastWriteTime -Descending |
           Select-Object -First 1
  if (-not $built) { throw 'msix output not found' }

  $dest = Join-Path $outDir "biumind-$version-$arch.msix"
  Copy-Item $built.FullName $dest -Force
  Write-Host "[msix] done: $dest" -ForegroundColor Green
} finally {
  Pop-Location
}
