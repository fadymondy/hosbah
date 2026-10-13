// Firebase's native modules are linked per platform only when that platform's config file is in
// secrets/ (see plugins/with-firebase.js).
//
// Linked without its config, the iOS build fails outright: RNFB Crashlytics adds a build phase that
// uploads dSYMs and exits with "Could not get GOOGLE_APP_ID in Google Services file". Left unlinked,
// the JS side is unaffected: every Firebase call goes through src/lib/firebase.ts, which reports "not
// linked" and no-ops. So local and CI builds without Firebase credentials still build and run.
const fs = require("fs");
const path = require("path");

const has = (file) => fs.existsSync(path.join(__dirname, "secrets", file));
const ios = has("GoogleService-Info.plist");
const android = has("google-services.json");

const FIREBASE = ["app", "messaging", "crashlytics"].map((m) => `@react-native-firebase/${m}`);

const dependencies = {};
for (const name of FIREBASE) {
  const platforms = {};
  if (!ios) platforms.ios = null;
  if (!android) platforms.android = null;
  if (Object.keys(platforms).length) dependencies[name] = { platforms };
}

module.exports = { dependencies };
