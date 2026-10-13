// Firebase (push, Crashlytics, Analytics) — wired PER PLATFORM, only where its config file exists.
//
// The config files live in secrets/ (gitignored; on Codemagic they are decoded from secure
// variables before prebuild — codemagic.yaml). app.config.js sets ios/android.googleServicesFile
// only for the files that exist, and passes that here. A build without them still prebuilds and
// runs: Firebase is simply off (every JS call is guarded — src/lib/crash.ts firebaseReady()).
//
// Why not just list "@react-native-firebase/app" and "/crashlytics" in app.json:
//   1. Their plugins configure BOTH platforms at once and throw when either file is missing, so an
//      Android-only CI build (which only has google-services.json) could not prebuild iOS and vice
//      versa. Here each platform's half runs only when that platform's file exists.
//   2. RNFB's iOS AppDelegate mod inserts `FirebaseApp.configure()` right above
//      `factory.startReactNative(` — inside the block plugins/with-scene-lifecycle.js removes (the
//      UIScene life cycle iOS 27 requires), so the two cannot both run. This plugin adds the call at
//      the top of didFinishLaunching instead, which the scene plugin leaves alone.
//
// The Android halves and the iOS plist copy are RNFB's own mods (loaded from the installed package,
// so their Gradle plugin versions stay in step with the JS SDK). A layout change there fails the
// prebuild loudly rather than shipping a build without Firebase.
const fs = require("fs");
const path = require("path");
const { withAppDelegate, withEntitlementsPlist, withInfoPlist, withPlugins } = require("expo/config-plugins");

function rnfbPluginPart(pkg, sub) {
  const root = path.dirname(require.resolve(`${pkg}/package.json`));
  const file = path.join(root, "plugin", "build", sub);
  if (!fs.existsSync(`${file}.js`) && !fs.existsSync(path.join(file, "index.js"))) {
    throw new Error(`with-firebase: ${pkg} no longer ships plugin/build/${sub}; update plugins/with-firebase.js`);
  }
  return require(file);
}

const DID_FINISH = /didFinishLaunchingWithOptions launchOptions:[^\n]*\n\s*\)\s*->\s*Bool\s*\{\n/;
const FIRST_IMPORT = /^(?:(?:internal|public|private|fileprivate)\s+)?import\s+\w+/m;

function withFirebaseConfigure(config) {
  return withAppDelegate(config, (c) => {
    if (c.modResults.language !== "swift") throw new Error("with-firebase: expected a Swift AppDelegate");
    let src = c.modResults.contents;
    if (!/^import FirebaseCore$/m.test(src)) {
      const m = FIRST_IMPORT.exec(src);
      if (!m) throw new Error("with-firebase: no import in AppDelegate.swift");
      src = `${src.slice(0, m.index)}import FirebaseCore\n${src.slice(m.index)}`;
    }
    if (!src.includes("FirebaseApp.configure()")) {
      const m = DID_FINISH.exec(src);
      if (!m) throw new Error("with-firebase: AppDelegate didFinishLaunching changed shape; update plugins/with-firebase.js");
      const at = m.index + m[0].length;
      src = `${src.slice(0, at)}    // Firebase first, before React Native starts (plugins/with-firebase.js).\n    FirebaseApp.configure()\n\n${src.slice(at)}`;
    }
    c.modResults.contents = src;
    return c;
  });
}

function withPushCapability(config, apsEnvironment) {
  config = withEntitlementsPlist(config, (c) => {
    c.modResults["aps-environment"] = apsEnvironment;
    return c;
  });
  return withInfoPlist(config, (c) => {
    const modes = new Set(c.modResults.UIBackgroundModes ?? []);
    modes.add("remote-notification");
    c.modResults.UIBackgroundModes = [...modes];
    return c;
  });
}

module.exports = function withFirebase(config, { ios = false, android = false, notificationIcon, notificationColor } = {}) {
  const plugins = [];
  if (ios) {
    const rnfbIos = rnfbPluginPart("@react-native-firebase/app", "ios");
    plugins.push(
      rnfbIos.withIosGoogleServicesFile, // copies secrets/GoogleService-Info.plist into the Xcode project
      withFirebaseConfigure,
      [withPushCapability, process.env.APS_ENVIRONMENT === "development" ? "development" : "production"],
    );
  }
  if (android) {
    const app = rnfbPluginPart("@react-native-firebase/app", "android");
    const crashlytics = rnfbPluginPart("@react-native-firebase/crashlytics", "android");
    const messaging = rnfbPluginPart("@react-native-firebase/messaging", "android");
    plugins.push(
      app.withBuildscriptDependency,
      app.withApplyGoogleServicesPlugin,
      app.withCopyAndroidGoogleServices,
      crashlytics.withBuildscriptDependency,
      crashlytics.withApplyCrashlyticsPlugin,
      // Manifest meta-data → @drawable/notification_icon, @color/notification_icon_color
      // (the resources themselves come from plugins/with-notification-icon.js).
      [messaging.withExpoPluginFirebaseNotification, { android: { notificationIcon, notificationColor } }],
    );
  }
  return withPlugins(config, plugins);
};
