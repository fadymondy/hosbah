// Release signing for store builds (Codemagic).
//
// `expo prebuild` generates android/app/build.gradle with the RELEASE build type signed by the
// DEBUG keystore — fine for an emulator, rejected by Play. Codemagic's android_signing exposes the
// upload keystore as CM_KEYSTORE_PATH / CM_KEYSTORE_PASSWORD / CM_KEY_ALIAS / CM_KEY_PASSWORD;
// this adds a `release` signing config that reads them and uses it only when they are present, so
// a local `assembleRelease` without a keystore still builds (debug-signed) instead of failing.
const { withAppBuildGradle } = require('expo/config-plugins');

const MARKER = 'CM_KEYSTORE_PATH';

module.exports = function withReleaseSigning(config) {
  return withAppBuildGradle(config, (cfg) => {
    let src = cfg.modResults.contents;
    if (src.includes(MARKER)) return cfg; // already applied

    const withConfig = src.replace(
      /signingConfigs\s*\{/,
      `signingConfigs {
        release {
            if (System.getenv("CM_KEYSTORE_PATH")) {
                storeFile file(System.getenv("CM_KEYSTORE_PATH"))
                storePassword System.getenv("CM_KEYSTORE_PASSWORD")
                keyAlias System.getenv("CM_KEY_ALIAS")
                keyPassword System.getenv("CM_KEY_PASSWORD")
            }
        }`,
    );
    const withRelease = withConfig.replace(
      /(buildTypes\s*\{[\s\S]*?release\s*\{[\s\S]*?)signingConfig signingConfigs\.debug/,
      '$1signingConfig System.getenv("CM_KEYSTORE_PATH") ? signingConfigs.release : signingConfigs.debug',
    );

    // Fail the prebuild loudly if the template changed shape: a store build that silently ships
    // debug-signed is only discovered when Play rejects the upload.
    if (withConfig === src || withRelease === withConfig) {
      throw new Error('with-release-signing: build.gradle no longer matches the expected signingConfigs/buildTypes shape');
    }
    cfg.modResults.contents = withRelease;
    return cfg;
  });
};
