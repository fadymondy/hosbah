// iOS: make React Native Firebase resolve Firebase through CocoaPods, not Swift Package Manager.
//
// React Native Firebase needs `use_frameworks! :linkage => :static` (app.json → expo-build-properties),
// and by default it pulls firebase-ios-sdk in through SPM. The two cannot be combined: each RNFB pod
// embeds its own copy of the SPM products and they collide at link time, so `pod install` refuses
// ("SPM + static linkage is not supported"). RNFB's fix is `$RNFirebaseDisableSPM = true` before any
// target block; this writes it at the top of the generated Podfile.
//
// Always applied — not only when the Firebase config files exist (plugins/with-firebase.js): the RNFB
// pods are autolinked from package.json in every build, configured or not.
// Ported from fadymondy.com/mobile/plugins/with-rnfb-disable-spm.js.
const { withDangerousMod } = require("expo/config-plugins");
const fs = require("fs");
const path = require("path");

const LINE = "$RNFirebaseDisableSPM = true";

module.exports = function withRnfbDisableSpm(config) {
  return withDangerousMod(config, [
    "ios",
    async (cfg) => {
      const podfile = path.join(cfg.modRequest.platformProjectRoot, "Podfile");
      const src = fs.readFileSync(podfile, "utf8");
      if (!src.includes(LINE)) {
        fs.writeFileSync(
          podfile,
          `# React Native Firebase via CocoaPods, not SPM — SPM + static frameworks cannot link (plugins/with-rnfb-disable-spm.js)\n${LINE}\n\n${src}`,
        );
      }
      return cfg;
    },
  ]);
};
