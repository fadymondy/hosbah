import { rnfbApp, rnfbMessaging } from "@/lib/firebase";

// FCM background handler. Loaded by index.js BEFORE expo-router's entry: on Android a data message can
// start the JS runtime headless, and the handler has to be registered by then (otherwise RNFB warns
// that none exists). The OS draws notification messages itself and the tap is routed by watchPush, so
// there is nothing to do here. Kept dependency-light on purpose: it runs without the component tree.
// Guarded, because a build without the Firebase config files has no native default app.
try {
  const app = rnfbApp();
  const messaging = rnfbMessaging();
  if (app && messaging && app.getApps().length > 0) {
    messaging.setBackgroundMessageHandler(messaging.getMessaging(), async () => undefined);
  }
} catch {
  // No Firebase in this build: push is off, the app is not.
}

export {};
