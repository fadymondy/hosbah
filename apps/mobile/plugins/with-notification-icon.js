// Android notification icon + colour for Firebase Cloud Messaging.
//
// @react-native-firebase/messaging only adds the manifest meta-data pointing at
// `@drawable/notification_icon` / `@color/notification_icon_color`; it never creates them. Expo SDK 57
// no longer accepts the top-level `notification` field, and `expo-notifications` would register its
// own FirebaseMessagingService, competing with React Native Firebase's. So this plugin creates the
// two resources itself, and plugins/with-firebase.js passes the same values to the messaging plugin
// so it adds the meta-data. Android renders only the icon's alpha channel: white on transparent.
//
// Always applied (the resources are harmless without Firebase). The icon is rendered by
// scripts/make-icons.mjs from the Hosbah mark.
const fs = require("fs");
const path = require("path");
const { withAndroidColors, withDangerousMod, AndroidConfig } = require("expo/config-plugins");

const DENSITIES = ["mdpi", "hdpi", "xhdpi", "xxhdpi", "xxxhdpi"];

module.exports = function withNotificationIcon(config, { icon, color }) {
  config = withDangerousMod(config, [
    "android",
    async (cfg) => {
      const src = path.resolve(cfg.modRequest.projectRoot, icon);
      if (!fs.existsSync(src)) throw new Error(`with-notification-icon: ${icon} does not exist (run node scripts/make-icons.mjs)`);
      const res = path.join(cfg.modRequest.platformProjectRoot, "app", "src", "main", "res");
      // One 96px (xxxhdpi) source for every density: Android scales a single bitmap cleanly enough
      // for a 24dp glyph, and the cube mark is crisp-edged.
      for (const density of DENSITIES) {
        const dir = path.join(res, `drawable-${density}`);
        fs.mkdirSync(dir, { recursive: true });
        fs.copyFileSync(src, path.join(dir, "notification_icon.png"));
      }
      return cfg;
    },
  ]);

  return withAndroidColors(config, (cfg) => {
    cfg.modResults = AndroidConfig.Colors.assignColorValue(cfg.modResults, {
      name: "notification_icon_color",
      value: color,
    });
    return cfg;
  });
};
