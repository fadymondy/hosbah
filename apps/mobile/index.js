// App entry. Firebase's background message handler must be registered before the app mounts (on
// Android it runs headless, with no React tree), so it is required ahead of expo-router's entry, and
// with require so it is not hoisted below it.
// The shared client draws CSRF tokens from crypto.getRandomValues, which Hermes may not provide.
if (!globalThis.crypto?.getRandomValues) {
  const { getRandomValues } = require("expo-crypto");
  globalThis.crypto = Object.assign(globalThis.crypto ?? {}, { getRandomValues });
}
require("./src/lib/push-background");
require("expo-router/entry");
