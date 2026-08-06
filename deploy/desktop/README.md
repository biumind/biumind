# Desktop release packaging

Scripts to produce distributable BiuMind desktop builds.

```
deploy/desktop/
├── macos/build_dmg.sh           ad-hoc + Developer ID DMG
├── linux/build_appimage.sh      AppImage (x86_64 / arm64)
└── windows/build_msix.ps1       MSIX package
```

Each script is independent and run on its target OS — Flutter does not
support cross-building desktop targets.

## macOS — DMG

```bash
# ad-hoc signed (default), runs locally on this Mac
deploy/desktop/macos/build_dmg.sh

# Developer ID signed + auto-notarized (CI / public release)
export DEVELOPER_ID='Developer ID Application: Acme Inc. (TEAMID)'
export APPLE_ID='you@example.com'
export APPLE_TEAM_ID='TEAMID'
export APPLE_PASSWORD='xxxx-xxxx-xxxx-xxxx'   # app-specific password
deploy/desktop/macos/build_dmg.sh developer-id
```

Output: `build/macos/biumind-<version>-<arch>.dmg`

The script uses `hdiutil` directly — no `create-dmg` dependency. Stage layout
is the standard "drag to Applications" symlink.

**Verified locally**: 19 MB DMG, ad-hoc signed bundle, mounts cleanly on
macOS 15.5, launches from both `/Applications` and the build folder.
App identifier is `app.biu.biumind`, universal arm64 + x86_64 binary.

### macOS 15+ ad-hoc signing gotcha

Three things bit us during P3.6 (documented so the next engineer doesn't):

1. **`codesign --deep` is broken on Flutter bundles.** Flutter's macOS
   release contains 7 frameworks under `Contents/Frameworks/`; `--deep`
   re-signs them inconsistently and dyld at launch reports "different
   Team IDs". The script signs each framework individually inside-out,
   then the main bundle.
2. **Hardened runtime + ad-hoc + `/Applications` = launch crash.**
   `--options=runtime` is required for Developer ID notarization, but it
   makes Gatekeeper enforce Team ID matching even for ad-hoc binaries
   when launched from `/Applications`. Local ad-hoc builds skip
   `--options=runtime`; real Developer ID builds keep it (notarization
   demands it).
3. **DMG made by `create-dmg` fails to unmount cleanly** in CI. The
   script uses pure `hdiutil create` with a `mktemp -d` staging folder
   and an `Applications` symlink — no AppleScript, no Finder layout,
   always builds.

### What's missing for App Store / public distribution

- **Apple Developer ID** ($99/yr) — needed for `developer-id` mode + notarization.
- **Hardened runtime entitlements review** — currently only
  `com.apple.security.network.client` and `com.apple.security.app-sandbox` are
  enabled. If we ever start writing user files outside the sandbox container we
  need `com.apple.security.files.user-selected.read-write`.
- **Sparkle / auto-update** — out of scope for alpha; ship as manual download
  for now.

## Linux — AppImage

```bash
# Run on a Linux host (amd64 or arm64). Prereqs:
sudo apt-get install -y clang cmake ninja-build pkg-config \
  libgtk-3-dev liblzma-dev libstdc++-12-dev libsecret-1-dev libsqlite3-dev

deploy/desktop/linux/build_appimage.sh
```

Output: `build/linux/biumind-<version>-<arch>.AppImage`

The script downloads `appimagetool` to `/tmp` if not on `$PATH`. AppImage runs
on any glibc 2.31+ distro (Debian 11, Ubuntu 20.04+, Fedora 33+).

**Not verified on this checkout** — needs a Linux host. CI matrix should add a
`linux-x86_64` runner that exercises the script per release.

## Windows — MSIX

```powershell
# Run in an elevated PowerShell on Windows with VS 2022 (Desktop C++ workload).
# Optional signing inputs:
$env:BIUMIND_CERT_PATH = 'C:\certs\biumind.pfx'
$env:BIUMIND_CERT_PASSWORD = 'xxx'
$env:BIUMIND_PUBLISHER = 'CN=BiuMind Inc., O=BiuMind Inc., C=US'

deploy\desktop\windows\build_msix.ps1
```

Output: `build/windows/biumind-<version>-<arch>.msix`

The package config lives in `apps/client/pubspec.yaml` under `msix_config`.
Without `BIUMIND_CERT_PATH` the MSIX is unsigned — Windows side-loads it but
SmartScreen will warn until either (a) Microsoft Store listing or (b)
EV codesigning cert provides reputation.

**Not verified on this checkout** — needs a Windows host. CI matrix should add
a `windows-x86_64` runner.

### Microsoft Store distribution

Requires:
- Microsoft Partner Center developer account ($19 one-time individual / $99 company).
- Identity name reservation (`app.biu.biumind` is the placeholder; real
  reservation gives a `<Id>` like `12345Author.BiuMind`).
- Submission via Partner Center after first MSIX build is uploaded.

## Versioning

All three scripts read the version from `apps/client/pubspec.yaml`:

```yaml
version: 0.1.0+1
```

Bump the `0.1.0` part for human-visible releases; the `+1` build number bumps
on every CI run.

## Status

| Target  | Built locally | Notarized / Signed | CI exists |
|---------|---------------|--------------------|-----------|
| macOS   | ✅ ad-hoc     | ⏳ awaiting Dev ID | no        |
| Linux   | ⏳            | n/a                | no        |
| Windows | ⏳            | ⏳ awaiting cert   | no        |

CI matrix wiring is part of the next release-engineering pass.
