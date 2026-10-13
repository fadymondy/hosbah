import { Platform, TurboModuleRegistry } from "react-native";

// The one door to @react-native-firebase. Its JS modules look their native
// TurboModule up with getEnforcing() the moment they are imported, so a static
// `import … from "@react-native-firebase/…"` kills the app at launch in a build
// where Firebase is not linked — which is every build without the config files
// in secrets/ (react-native.config.js leaves the native side out, because
// Crashlytics' build phase fails without GoogleService-Info.plist).
//
// So nothing imports Firebase at module level: these loaders check the native
// module is registered first, then require() on demand. Types only are imported
// statically.

type AppModule = typeof import("@react-native-firebase/app");
type CrashlyticsModule = typeof import("@react-native-firebase/crashlytics");
type MessagingModule = typeof import("@react-native-firebase/messaging");

let linked: boolean | undefined;

/** True when this binary has Firebase's native modules linked. */
export function firebaseLinked(): boolean {
  if (linked === undefined) {
    try {
      linked = Platform.OS !== "web" && TurboModuleRegistry.get("NativeRNFBTurboApp") != null;
    } catch {
      linked = false;
    }
  }
  return linked;
}

function loader<T>(load: () => T): () => T | null {
  let mod: T | null | undefined;
  return () => {
    if (mod === undefined) {
      try {
        mod = firebaseLinked() ? load() : null;
      } catch {
        mod = null;
      }
    }
    return mod;
  };
}

export const rnfbApp = loader<AppModule>(() => require("@react-native-firebase/app"));
export const rnfbCrashlytics = loader<CrashlyticsModule>(() => require("@react-native-firebase/crashlytics"));
export const rnfbMessaging = loader<MessagingModule>(() => require("@react-native-firebase/messaging"));
