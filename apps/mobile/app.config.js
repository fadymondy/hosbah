// Hosbah phone app. One config file: static identity first, then what depends on the build environment
// and on files that are not in git.
//
// Firebase (push, Crashlytics): the config files live in secrets/ (gitignored; Codemagic decodes them
// from GOOGLE_SERVICES_JSON / GOOGLE_SERVICE_INFO_PLIST before prebuild). Each platform is wired only
// when ITS file exists (plugins/with-firebase.js, react-native.config.js), so a checkout or CI run
// without them still prebuilds and runs, with push and Crashlytics off.
const fs = require("fs");
const path = require("path");

const ID = "com.fadymondy.hosbah";
const IOS_FIREBASE = "./secrets/GoogleService-Info.plist";
const ANDROID_FIREBASE = "./secrets/google-services.json";
const APPLE_TEAM_ID = process.env.APPLE_TEAM_ID || "JW9HJH86GC";
const BRAND = "#2e6f9e";
const has = (rel) => fs.existsSync(path.join(__dirname, rel));

// Google Sign-In on iOS needs the iOS OAuth client's reversed id as a URL scheme. It comes from the build
// environment (never hard-coded); without it the plugin is left out and the app hides the Google button
// on iOS (src/lib/social.ts). Android needs nothing here: its client is matched by package + SHA-1.
const GOOGLE_IOS_CLIENT_ID = process.env.EXPO_PUBLIC_GOOGLE_IOS_CLIENT_ID || "";
const googleIosUrlScheme = GOOGLE_IOS_CLIENT_ID
  ? "com.googleusercontent.apps." + GOOGLE_IOS_CLIENT_ID.replace(/\.apps\.googleusercontent\.com$/, "")
  : null;
const asset = (f) => `./assets/brand/hosbah-${f}.png`;

module.exports = () => {
  const ios = has(IOS_FIREBASE);
  const android = has(ANDROID_FIREBASE);
  return {
    name: "حوسبة",
    slug: "hosbah",
    version: "1.0.0",
    orientation: "portrait",
    icon: asset("icon"),
    // hosbah://t/<team>/apps/<id> ... (see src/lib/links.ts and src/app/+native-intent.ts)
    scheme: "hosbah",
    userInterfaceStyle: "automatic",
    // expo-updates stays installed for the language switch's one controlled reload (RTL), but is off:
    // with an updates URL it would own bundle loading.
    updates: { enabled: false },
    ios: {
      bundleIdentifier: ID,
      buildNumber: process.env.BUILD_NUMBER || "1",
      appleTeamId: APPLE_TEAM_ID,
      supportsTablet: false,
      // Sign in with Apple (com.apple.developer.applesignin entitlement); the App ID needs the capability.
      usesAppleSignIn: true,
      infoPlist: {
        ITSAppUsesNonExemptEncryption: false,
      },
      ...(ios ? { googleServicesFile: IOS_FIREBASE } : {}),
    },
    android: {
      package: ID,
      versionCode: Number(process.env.BUILD_NUMBER) || 1,
      adaptiveIcon: {
        backgroundColor: "#f0ebe1",
        foregroundImage: asset("adaptive-fg"),
        monochromeImage: asset("adaptive-mono"),
      },
      predictiveBackGestureEnabled: false,
      permissions: ["android.permission.POST_NOTIFICATIONS", "android.permission.USE_BIOMETRIC", "android.permission.USE_FINGERPRINT"],
      blockedPermissions: ["com.google.android.gms.permission.AD_ID", "android.permission.RECORD_AUDIO", "android.permission.SYSTEM_ALERT_WINDOW"],
      ...(android ? { googleServicesFile: ANDROID_FIREBASE } : {}),
    },
    plugins: [
      "expo-router",
      "expo-secure-store",
      ["expo-local-authentication", { faceIDPermission: "Confirm deploys, restarts and stops with Face ID. Your face data never leaves this device." }],
      "expo-localization",
      "expo-font",
      "expo-apple-authentication",
      "expo-web-browser",
      ...(googleIosUrlScheme ? [["@react-native-google-signin/google-signin", { iosUrlScheme: googleIosUrlScheme }]] : []),
      [
        "expo-splash-screen",
        {
          backgroundColor: "#f0ebe1",
          image: asset("splash"),
          imageWidth: 120,
          resizeMode: "contain",
          dark: { backgroundColor: "#0b1429", image: asset("splash-dark") },
        },
      ],
      // React Native Firebase needs static frameworks on iOS.
      ["expo-build-properties", { ios: { useFrameworks: "static" } }],
      "./plugins/with-scene-lifecycle",
      "./plugins/with-gradle-memory",
      "./plugins/with-release-signing",
      "./plugins/with-rnfb-disable-spm",
      ["./plugins/with-notification-icon", { icon: "./assets/notification-icon.png", color: BRAND }],
      // Creates the "alerts" notification channel (high importance) the server addresses.
      ["./plugins/with-alerts-channel", { channelId: "alerts" }],
      ["./plugins/with-firebase", { ios, android, notificationIcon: "./assets/notification-icon.png", notificationColor: BRAND }],
    ],
    experiments: { typedRoutes: false },
    extra: {
      // Which platforms this build was configured with; diagnostics only. The app decides at runtime
      // from the native Firebase app (src/lib/firebase.ts).
      firebase: { ios, android },
      apiUrl: process.env.EXPO_PUBLIC_API_URL ?? null,
    },
  };
};
