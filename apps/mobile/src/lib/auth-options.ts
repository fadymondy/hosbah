import type { AuthProvider, SignupMode } from "@hosbah/client";
import { useEffect, useState } from "react";
import { Platform } from "react-native";

import { useApi } from "@/lib/api";
import { appleNativeAvailable } from "@/lib/oauth";

/** One sign-in button: "apple-native" is the iOS Apple sheet, anything else a browser round trip. */
export interface ProviderButton {
  id: string;
  name: string;
}

export interface AuthOptions {
  buttons: ProviderButton[];
  /** Null until the server answered (or when it could not say): sign-up stays offered. */
  signupMode: SignupMode | null;
}

/**
 * Which provider buttons to show, from what the server has configured (GET /api/auth/providers lists only
 * those, so an unconfigured provider never shows). On iOS, Apple is the native sheet, and Google/GitHub
 * appear only beside it (App Store guideline 4.8). Elsewhere Apple goes through the browser like the rest.
 */
export function providerButtons(list: AuthProvider[], os: string, appleNative: boolean): ProviderButton[] {
  const out: ProviderButton[] = [];
  const apple = list.find((p) => p.id === "apple");
  if (apple) {
    if (os === "ios") {
      if (apple.native && appleNative) out.push({ id: "apple-native", name: apple.name });
    } else if (apple.web) {
      out.push({ id: "apple", name: apple.name });
    }
  }
  const appleShown = out.length > 0;
  for (const p of list) {
    if (p.id === "apple" || !p.web) continue;
    if (os === "ios" && !appleShown) continue;
    out.push({ id: p.id, name: p.name });
  }
  return out;
}

/** The sign-in options of the server the app points at, refetched when that server changes. */
export function useAuthOptions(): AuthOptions {
  const { client } = useApi();
  const [opts, setOpts] = useState<AuthOptions>({ buttons: [], signupMode: null });
  useEffect(() => {
    let live = true;
    void (async () => {
      const [providers, settings, native] = await Promise.all([
        client.providers().catch(() => []),
        client.publicSettings().catch(() => null),
        Platform.OS === "ios" ? appleNativeAvailable() : Promise.resolve(false),
      ]);
      if (live) setOpts({ buttons: providerButtons(providers, Platform.OS, native), signupMode: settings?.signup_mode ?? null });
    })();
    return () => {
      live = false;
    };
  }, [client]);
  return opts;
}
