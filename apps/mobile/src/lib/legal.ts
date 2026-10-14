import type { Locale } from "@hosbah/client";
import * as WebBrowser from "expo-web-browser";

// The privacy policy and terms live on the public site (hosbah.com), in the app's language.
// App Store guideline 5.1.1 / Play User Data policy: both must be reachable inside the app.
export const SITE = "https://hosbah.com";

export type LegalPage = "privacy" | "terms";

export function legalUrl(page: LegalPage, locale: Locale): string {
  return `${SITE}/${locale === "ar" ? "ar" : "en"}/${page}`;
}

export function openLegal(page: LegalPage, locale: Locale): void {
  void WebBrowser.openBrowserAsync(legalUrl(page, locale)).catch(() => undefined);
}

/** The web console of the server this app talks to (teams are created and joined there). */
export function consoleUrl(serverUrl: string, locale: Locale): string {
  return `${serverUrl.replace(/\/+$/, "")}/${locale === "ar" ? "ar" : "en"}`;
}

export function openConsole(serverUrl: string, locale: Locale): void {
  void WebBrowser.openBrowserAsync(consoleUrl(serverUrl, locale)).catch(() => undefined);
}
