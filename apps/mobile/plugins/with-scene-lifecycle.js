// iOS 27 asserts at launch (EXC_BREAKPOINT in _UIApplicationEvaluateRuntimeIssueForNoSceneLifecycleAdoption)
// unless an app built with its SDK adopts the UIScene life cycle. Expo 57 ships the scene delegate
// (EXExpoAppSceneDelegate) but its prebuild template still creates the window in the app delegate,
// so wire it up here until the template does:
//   - Info.plist declares a single window scene handled by EXExpoAppSceneDelegate;
//   - AppDelegate conforms to ExpoReactNativeFactoryProvider and stops creating the window itself
//     (the scene delegate creates it and starts React Native into it).
const { withAppDelegate, withInfoPlist } = require("expo/config-plugins");

const WINDOW_BLOCK =
  /\n#if os\(iOS\) \|\| os\(tvOS\)\n\s*window = UIWindow\(frame: UIScreen\.main\.bounds\)\n\s*factory\.startReactNative\([\s\S]*?\n#endif\n/;

module.exports = function withSceneLifecycle(config) {
  config = withInfoPlist(config, (c) => {
    c.modResults.UIApplicationSceneManifest = {
      UIApplicationSupportsMultipleScenes: false,
      UISceneConfigurations: {
        UIWindowSceneSessionRoleApplication: [
          { UISceneConfigurationName: "Default Configuration", UISceneDelegateClassName: "EXExpoAppSceneDelegate" },
        ],
      },
    };
    return c;
  });

  return withAppDelegate(config, (c) => {
    if (c.modResults.language !== "swift") throw new Error("with-scene-lifecycle: expected a Swift AppDelegate");
    let src = c.modResults.contents;
    if (src.includes("ExpoReactNativeFactoryProvider")) return c;

    const decl = "class AppDelegate: ExpoAppDelegate {";
    if (!src.includes(decl) || !WINDOW_BLOCK.test(src)) {
      throw new Error("with-scene-lifecycle: the AppDelegate template changed; update the plugin");
    }
    src = src.replace(decl, "class AppDelegate: ExpoAppDelegate, ExpoReactNativeFactoryProvider {");
    src = src.replace(WINDOW_BLOCK, "\n    // The window is created by EXExpoAppSceneDelegate (plugins/with-scene-lifecycle.js).\n");
    c.modResults.contents = src;
    return c;
  });
};
