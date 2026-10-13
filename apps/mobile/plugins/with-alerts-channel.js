// Android: create the "alerts" notification channel (high importance) at app start, and make it FCM's
// default channel.
//
// The server sends every push with channel_id "alerts". On Android 8+ a notification addressed to a
// channel that does not exist is silently dropped to a low-priority default, so the channel must exist
// before the first push arrives, including while the app has never been opened (FCM then draws the
// notification itself, with no JS running). So it is created natively in MainApplication.onCreate,
// not from JS. expo-notifications is deliberately not used: it registers its own
// FirebaseMessagingService, which competes with React Native Firebase's.
const { withAndroidManifest, withMainApplication, AndroidConfig } = require("expo/config-plugins");

const MARK = "// hosbah: alerts channel";

module.exports = function withAlertsChannel(config, { channelId = "alerts", channelName = "Alerts" } = {}) {
  config = withAndroidManifest(config, (cfg) => {
    const app = AndroidConfig.Manifest.getMainApplicationOrThrow(cfg.modResults);
    AndroidConfig.Manifest.addMetaDataItemToMainApplication(app, "com.google.firebase.messaging.default_notification_channel_id", channelId);
    return cfg;
  });

  return withMainApplication(config, (cfg) => {
    if (cfg.modResults.language !== "kt") throw new Error("with-alerts-channel: expected a Kotlin MainApplication");
    let src = cfg.modResults.contents;
    if (src.includes(MARK)) return cfg;
    const re = /(override fun onCreate\(\)\s*\{\s*\n\s*super\.onCreate\(\)\s*\n)/;
    if (!re.test(src)) throw new Error("with-alerts-channel: MainApplication.onCreate changed shape; update plugins/with-alerts-channel.js");
    const block =
      `    ${MARK}\n` +
      `    if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.O) {\n` +
      `      val channel = android.app.NotificationChannel("${channelId}", "${channelName}", android.app.NotificationManager.IMPORTANCE_HIGH)\n` +
      `      channel.enableVibration(true)\n` +
      `      getSystemService(android.app.NotificationManager::class.java).createNotificationChannel(channel)\n` +
      `    }\n`;
    cfg.modResults.contents = src.replace(re, `$1${block}`);
    return cfg;
  });
};
