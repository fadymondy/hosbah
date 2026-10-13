# Releasing the Hosbah phone app

Two Codemagic workflows in the repo-root `codemagic.yaml`, both started by hand:

- `hosbah-android-internal`: builds an AAB and uploads it to the Google Play **internal** track as a draft.
- `hosbah-ios-testflight`: builds the IPA and sends it to TestFlight (`submit_to_testflight`).

Nothing here ever targets a review, production or App Store submission. Promote a build from the
store consoles yourself.

iOS archiving and signing happen on Fady's Mac or in Codemagic with his App Store Connect key
(team JW9HJH86GC); they are not done from this repo or from CI scripts committed here.

## Codemagic configuration (names only)

- Env groups: `hosbah_app_env`, `hosbah_android_keystore`, `hosbah_google_play`.
- Integration: `hosbah_asc` (App Store Connect API key).
- `hosbah_app_env` holds `GOOGLE_SERVICES_JSON` and `GOOGLE_SERVICE_INFO_PLIST` as base64. The build
  decodes them into `secrets/`, which is gitignored. Without them the app builds without push and Crashlytics.

The first Android upload of `com.fadymondy.hosbah` must be done once by hand in the Play Console.

## Local checks

```sh
npm ci && npx tsc --noEmit && npx jest
npm run mock        # Metro with fixture data, no server needed
```
