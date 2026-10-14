import * as SplashScreen from "expo-splash-screen";
import { Stack } from "expo-router";
import { StatusBar } from "expo-status-bar";
import { useEffect } from "react";
import { SafeAreaProvider } from "react-native-safe-area-context";
import { useQueryClient } from "@tanstack/react-query";

import { ApiProvider, useApi } from "@/lib/api";
import { LinkHandler } from "@/lib/link-handler";
import { PrefsProvider, usePrefs } from "@/lib/prefs";
import { watchPush } from "@/lib/push";
import { hydrateTeam } from "@/lib/team";
import { BannerHost } from "@/ui/banner-host";
import { ToastProvider } from "@/ui/toast";

void SplashScreen.preventAutoHideAsync();

export default function RootLayout() {
  return (
    <SafeAreaProvider>
      <PrefsProvider>
        <Shell />
      </PrefsProvider>
    </SafeAreaProvider>
  );
}

function Shell() {
  const { ready } = usePrefs();
  // Held on the native splash until the language, theme and fonts are in (and the RTL flag is right).
  if (!ready) return null;
  return (
    <ApiProvider>
      <ToastProvider>
        <Gate />
      </ToastProvider>
    </ApiProvider>
  );
}

function Gate() {
  const { status } = useApi();
  const { colors, theme } = usePrefs();

  useEffect(() => {
    void hydrateTeam();
  }, []);
  useEffect(() => {
    if (status !== "loading") void SplashScreen.hideAsync();
  }, [status]);

  if (status === "loading") return null;
  return (
    <>
      <StatusBar style={theme === "dark" ? "light" : "dark"} />
      <Stack screenOptions={{ headerShown: false, contentStyle: { backgroundColor: colors.bg } }}>
        <Stack.Protected guard={status === "in"}>
          <Stack.Screen name="(tabs)" />
          <Stack.Screen name="apps/[id]/index" />
          <Stack.Screen name="apps/[id]/deployments/[did]" />
          <Stack.Screen name="servers/[id]" />
          <Stack.Screen name="delete-account" />
        </Stack.Protected>
        <Stack.Protected guard={status === "out"}>
          <Stack.Screen name="sign-in" />
        </Stack.Protected>
      </Stack>
      {status === "in" ? <SignedInEffects /> : null}
      <BannerHost />
    </>
  );
}

/** Things that run only while signed in: push registration + tap routing, deep-link handling. */
function SignedInEffects() {
  const { client } = useApi();
  const { locale } = usePrefs();
  const qc = useQueryClient();
  useEffect(
    () => watchPush(client, () => locale, { askOnce: true, onReceived: () => void qc.invalidateQueries({ queryKey: ["notifications"] }) }),
    // Re-registering on a language change also tells the server the new language for pushes.
    [client, locale, qc],
  );
  return <LinkHandler />;
}
