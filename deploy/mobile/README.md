# Mobile release packaging

Scripts to produce distributable BiuMind mobile builds.

```
deploy/mobile/
├── android/build_release.sh      AAB + per-ABI APKs (Play Store / sideload)
└── ios/
    ├── build_ipa.sh              verify / appstore / adhoc IPA
    ├── ExportOptions-AppStore.plist
    └── ExportOptions-AdHoc.plist
```

## Android

```bash
# Default: signed Android App Bundle
deploy/mobile/android/build_release.sh             # → build/android/biumind-<v>.aab

# Per-ABI APKs (sideload / device testing)
deploy/mobile/android/build_release.sh apk

# Both
deploy/mobile/android/build_release.sh both
```

**Verified locally**: `biumind-0.1.0.aab` 45 MB, signed with the dev keystore
(`apps/client/android/biumind-dev.jks`), built via `flutter build appbundle
--release` against Flutter 3.27 + AGP target SDK 35.

### Signing flow

1. **First run** — script auto-generates `apps/client/android/biumind-dev.jks`
   plus `apps/client/android/key.properties` pointing to it. Both are
   gitignored. Use this for local CI dry-runs only.
2. **Production** — replace with the keystore Google Play assigned during the
   first manual upload (Google Play App Signing). Drop the `.jks` somewhere
   secure and update `key.properties`:
   ```properties
   storePassword=…
   keyPassword=…
   keyAlias=upload
   storeFile=/secrets/biumind-upload.jks
   ```
   Once the keystore matches the Play Console, re-run the script.

### Play Internal Testing flow

1. Create app in <https://play.google.com/console> with package name `app.biu.biumind`.
2. Internal testing → Create new release → upload `biumind-<version>.aab`.
3. Add tester emails (up to 100). Releases are live within ~minutes.
4. Bump `version: x.y.z+N` in `apps/client/pubspec.yaml` for each new build.

### What's missing for public Play Store

- **Privacy policy URL** + **Data safety form** in Play Console.
- **App content questionnaire**: ads / target audience / content rating / etc.
- **Production track promotion** with phased rollout (start at 1%).
- Optional: **Google Play App Signing** enrollment — recommended for the upload-key
  rotation story.

## iOS

```bash
# Verify the Xcode project compiles end-to-end (no Apple Dev ID required)
deploy/mobile/ios/build_ipa.sh verify

# Build an App Store / TestFlight IPA
APPLE_TEAM_ID=ABCDE12345 deploy/mobile/ios/build_ipa.sh appstore

# Build an ad-hoc IPA for a UDID-pinned set of test devices
APPLE_TEAM_ID=ABCDE12345 deploy/mobile/ios/build_ipa.sh adhoc
```

**Verified locally**: build script + Info.plist + ExportOptions plumbing in place.
**Not yet exercised on this host** — the verify build needs an iOS simulator
runtime (Xcode → Settings → Components → iOS 18.5+) which isn't installed.
The `appstore` / `adhoc` modes need an Apple Developer ID we don't have yet.
CI matrix that runs on a fresh GitHub-hosted macOS runner will exercise both.

### TestFlight flow

1. Apple Developer Program enrollment ($99/yr) + register `app.biu.biumind` in
   <https://developer.apple.com/account/resources/identifiers>.
2. Create an app in App Store Connect with the same Bundle ID.
3. `APPLE_TEAM_ID=… deploy/mobile/ios/build_ipa.sh appstore`
4. Upload via Transporter.app or:
   ```bash
   xcrun altool --upload-app -f build/ios/biumind-*-appstore.ipa \
     -u apple-id@example.com -p <app-specific-password>
   ```
5. App Store Connect → TestFlight → wait for processing → invite testers.

### Permissions / entitlements wired

- `Info.plist`:
  - `NSAppTransportSecurity / NSAllowsLocalNetworking=true` — model-relay on
    `localhost` / LAN works without HTTPS.
  - `NSExceptionDomains.localhost` — explicit allow for `http://localhost:7001`.
  - `ITSAppUsesNonExemptEncryption=false` — skips the export-compliance
    question on every TestFlight upload.
- iOS keychain access uses `flutter_secure_storage` defaults (no extra
  entitlement needed beyond automatic provisioning).

### Permissions wired (Android)

- `AndroidManifest.xml`:
  - `INTERNET` — model-relay HTTPS / SSE
  - `ACCESS_NETWORK_STATE` — pingHub diagnostic
  - `networkSecurityConfig=@xml/network_security_config` — cleartext allowed
    only for `localhost`, `127.0.0.1`, `10.0.2.2` (emulator host bridge), and
    RFC 1918 LAN ranges. Everything else still requires HTTPS.

## Versioning

```yaml
# apps/client/pubspec.yaml
version: 0.1.0+1   # 0.1.0 → versionName, +1 → versionCode (Android) / build number (iOS)
```

Bump `+N` for every CI run going to internal testing; bump `0.1.0` for
human-visible releases.

## Status

| Target  | Built locally | Signed                      | CI exists |
|---------|---------------|-----------------------------|-----------|
| Android | ✅ AAB 45 MB  | ✅ dev keystore             | no        |
| iOS     | ⏳ (needs simulator runtime) | ⏳ awaiting Dev ID | no |

CI matrix that exercises both is part of the next release-engineering pass —
goal is a single tag push triggering Android AAB upload to Play Internal
Testing + iOS IPA upload to TestFlight.
