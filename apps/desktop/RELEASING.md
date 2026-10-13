# Releasing Hosbah desktop

App id `com.fadymondy.hosbah.desktop`, product name "Hosbah", protocol `hosbah://`.
Nothing here publishes anything: `publish` is configured (GitHub draft releases) but every command passes `--publish never`.

## Versions

The version is `apps/desktop/package.json` `version`. Bump it, commit, then build:

```sh
cd apps/desktop
npm version 0.2.0 --no-git-tag-version   # or edit package.json by hand
```

Installer names carry the version (`Hosbah-Setup-0.2.0.exe`), so there is nothing else to change.

## Windows (NSIS, x64)

From `apps/`:

```sh
npm install
cd desktop
npm run typecheck && npm test
npm run build                                   # renderer (vite) + main/preload (esbuild) into dist/
npx electron-builder --win nsis --x64 --publish never
```

Output in `release/`: `Hosbah-Setup-<version>.exe` (+ `.blockmap`) and `win-unpacked/`.
`npm run package:dir` builds only the unpacked app (fast check). The installer is unsigned unless you provide a code-signing certificate through electron-builder's standard `CSC_LINK` / `CSC_KEY_PASSWORD` environment variables (from the vault, never inline or committed). Unsigned installers show a SmartScreen warning.

## macOS (dmg, universal) - must run on the Mac

Windows cannot produce or notarize a mac build. On the Mac:

```sh
git pull && cd apps && npm install && cd desktop
npm run typecheck && npm test
export APPLE_ID=...                     # vault entry: apple-developer-id
export APPLE_APP_SPECIFIC_PASSWORD=...  # vault entry: apple-app-specific-password
export APPLE_TEAM_ID=...                # vault entry: apple-team-id
export CSC_NAME="Developer ID Application: <name> (<team id>)"   # or CSC_LINK + CSC_KEY_PASSWORD
npm run package:mac                     # dmg + zip, universal (x64 + arm64), hardened runtime
```

Read the three values from the vault (names above), do not paste them into scripts, shell history files or commits.
`scripts/notarize.cjs` runs as `afterSign`. It notarizes only when all three `APPLE_*` variables are set; otherwise it prints a loud skip message and the build still succeeds (an unnotarized dmg is blocked by Gatekeeper on other Macs). Entitlements are in `build/entitlements.mac.plist`.
Verify afterwards: `spctl -a -vv release/mac-universal/Hosbah.app` and `xcrun stapler validate Hosbah-*.dmg`.

Touch ID confirmation for app actions uses `systemPreferences.promptTouchID` on the Mac and falls back to the typed-name dialog when unavailable.

## Linux (optional)

`npx electron-builder --linux AppImage --publish never` on a Linux host.

## Testing without touching production

```sh
npm run mock        # local API on :8710 (demo@example.com / demo-pass)
npm run dev         # point Settings -> Server address at http://localhost:8710
npm run shots       # drives the real app against the mock and writes screenshots + a smoke report
```

`HOSBAH_E2E=1` (set by `npm run shots`) refuses any non-localhost API address. Never run redeploy / restart / start / stop against a real server while testing.
