import type { Locale } from "@hosbah/client";
import { palettes, type Palette } from "@hosbah/client/brand";
import { DEFAULT_LOCALE, isRTL, pickLocale, t as translate, type MessageKey } from "@hosbah/client/i18n";
import { Inter_400Regular, Inter_500Medium, Inter_600SemiBold } from "@expo-google-fonts/inter";
import { JetBrainsMono_400Regular } from "@expo-google-fonts/jetbrains-mono";
import { useFonts } from "expo-font";
import * as Updates from "expo-updates";
import { getLocales } from "expo-localization";
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { DevSettings, I18nManager, useColorScheme } from "react-native";

import { getStored, KEYS, removeStored, setStored } from "@/lib/storage";

export type ThemeSetting = "light" | "dark" | "system";
export type TFn = (key: MessageKey, vars?: Record<string, string | number>) => string;

/**
 * RTL is a native layout flag that only applies after a restart. Returns true when a reload was
 * triggered.
 *
 * The direction is OURS to set: app.config.js must not give expo-localization a `supportsRTL` option.
 * With it, expo-localization's iOS module re-runs forceRTL(<device language is RTL>) on every launch,
 * resetting the in-app Arabic choice to LTR on an English iPhone (an endless reload/splash freeze in
 * another app of ours).
 *
 * At most ONE reload per direction change, so a platform that refuses the flag can never loop: if the
 * direction still disagrees after the one reload, the app carries on in the chosen language.
 */
export async function applyDirection(locale: Locale): Promise<boolean> {
  const rtl = isRTL(locale);
  if (I18nManager.isRTL === rtl) {
    await removeStored(KEYS.rtlReload);
    return false;
  }
  I18nManager.allowRTL(rtl);
  I18nManager.forceRTL(rtl);
  if ((await getStored(KEYS.rtlReload)) === locale) return false;
  await setStored(KEYS.rtlReload, locale);
  try {
    await Updates.reloadAsync();
  } catch {
    // Development builds without an updates runtime fall back to the dev reload.
    DevSettings.reload();
  }
  return true;
}

interface Prefs {
  ready: boolean;
  locale: Locale;
  theme: "light" | "dark";
  themeSetting: ThemeSetting;
  colors: Palette;
  rtl: boolean;
  t: TFn;
  setLocale: (l: Locale) => Promise<void>;
  setTheme: (t: ThemeSetting) => Promise<void>;
}

const Ctx = createContext<Prefs | null>(null);

/** Arabic first: the device's language only matters when it is English. */
function deviceLocale(): Locale {
  try {
    return pickLocale(getLocales()[0]?.languageTag);
  } catch {
    return DEFAULT_LOCALE;
  }
}

export function PrefsProvider({ children }: { children: ReactNode }) {
  const system = useColorScheme();
  const [locale, setLocaleState] = useState<Locale>(deviceLocale());
  const [themeSetting, setThemeSetting] = useState<ThemeSetting>("system");
  const [loaded, setLoaded] = useState(false);
  const [fontsLoaded, fontError] = useFonts({
    "Lusail-Light": require("../../assets/fonts/Lusail-Light.ttf"),
    "Lusail-Regular": require("../../assets/fonts/Lusail-Regular.ttf"),
    "Lusail-Medium": require("../../assets/fonts/Lusail-Medium.ttf"),
    Inter_400Regular,
    Inter_500Medium,
    Inter_600SemiBold,
    JetBrainsMono_400Regular,
  });

  useEffect(() => {
    void (async () => {
      const [l, th] = await Promise.all([getStored(KEYS.locale), getStored(KEYS.theme)]);
      if (th === "dark" || th === "light" || th === "system") setThemeSetting(th);
      const chosen: Locale = l === "ar" || l === "en" ? l : deviceLocale();
      setLocaleState(chosen);
      // Keep the native direction in step with the stored (or default) language.
      if (await applyDirection(chosen)) return; // reloading: stay on the splash
      setLoaded(true);
    })();
  }, []);

  const setLocale = useCallback(async (l: Locale) => {
    await setStored(KEYS.locale, l);
    setLocaleState(l);
    await applyDirection(l);
  }, []);

  const setTheme = useCallback(async (th: ThemeSetting) => {
    await setStored(KEYS.theme, th);
    setThemeSetting(th);
  }, []);

  const theme: "light" | "dark" = themeSetting === "system" ? (system === "dark" ? "dark" : "light") : themeSetting;
  const t = useCallback<TFn>((key, vars) => translate(locale, key, vars), [locale]);

  const value = useMemo<Prefs>(
    () => ({
      ready: loaded && (fontsLoaded || !!fontError),
      locale,
      theme,
      themeSetting,
      colors: palettes[theme],
      rtl: I18nManager.isRTL,
      t,
      setLocale,
      setTheme,
    }),
    [loaded, fontsLoaded, fontError, locale, theme, themeSetting, t, setLocale, setTheme],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function usePrefs(): Prefs {
  const v = useContext(Ctx);
  if (!v) throw new Error("usePrefs outside PrefsProvider");
  return v;
}

export const useT = (): TFn => usePrefs().t;
export const useColors = (): Palette => usePrefs().colors;

/** Font family for a weight, by language: Lusail for Arabic, Inter for English. */
export function fontFor(locale: Locale, weight: "regular" | "medium" | "strong" | "mono"): string {
  if (weight === "mono") return "JetBrainsMono_400Regular";
  if (locale === "ar") return weight === "regular" ? "Lusail-Regular" : "Lusail-Medium";
  return weight === "regular" ? "Inter_400Regular" : weight === "medium" ? "Inter_500Medium" : "Inter_600SemiBold";
}
