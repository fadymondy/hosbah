import * as AppleAuthentication from "expo-apple-authentication";
import * as Crypto from "expo-crypto";
import * as WebBrowser from "expo-web-browser";
import { ApiError, type HosbahClient, type LoginResult } from "@hosbah/client";

/**
 * Where the server sends the system browser back at the end of a provider round trip:
 * hosbah://oauth?code=<one-time code> or hosbah://oauth?error=<code>. The auth session watches for this
 * prefix and closes itself, so the browser never stays open on a "you can go back now" page.
 */
export const OAUTH_RETURN = "hosbah://oauth";

export function randomHex(bytes = 32): string {
  return Array.from(Crypto.getRandomBytes(bytes), (b) => b.toString(16).padStart(2, "0")).join("");
}

export function sha256Hex(s: string): Promise<string> {
  return Crypto.digestStringAsync(Crypto.CryptoDigestAlgorithm.SHA256, s, { encoding: Crypto.CryptoEncoding.HEX });
}

/** Whether a URL the OS handed the app is the provider round trip coming back (any platform's shape of it). */
export function isOAuthReturn(url: string): boolean {
  return /^(hosbah:\/\/|\/+)oauth(\/|\?|$)/.test(url);
}

/** The code or error carried by hosbah://oauth?…, or null for any other URL. */
export function parseOAuthReturn(url: string): { code: string } | { error: string } | null {
  if (!isOAuthReturn(url)) return null;
  const q = url.split("?")[1]?.split("#")[0] ?? "";
  const params: Record<string, string> = {};
  for (const pair of q.split("&")) {
    const [k, v = ""] = pair.split("=");
    if (k) params[decodeURIComponent(k)] = decodeURIComponent(v.replace(/\+/g, " "));
  }
  if (params.code) return { code: params.code };
  return { error: params.error || "provider_error" };
}

type Browser = Pick<typeof WebBrowser, "openAuthSessionAsync">;

/**
 * Google, GitHub (and Apple on Android) through the system browser. The app keeps a random verifier and
 * sends only its SHA-256, so the one-time code in the return URL is useless to anything else that
 * catches hosbah://oauth. Resolves null when the person backs out; throws ApiError (code = the server's
 * error, e.g. signup_closed, oauth_email_unverified) otherwise, or 2fa_required from the exchange.
 */
export async function signInWithBrowser(client: HosbahClient, provider: string, locale: string, browser: Browser = WebBrowser): Promise<LoginResult | null> {
  const verifier = randomHex(32);
  const challenge = await sha256Hex(verifier);
  const res = await browser.openAuthSessionAsync(client.oauthStartUrl(provider, challenge, locale), OAUTH_RETURN);
  if (res.type !== "success") return null;
  const back = parseOAuthReturn(res.url);
  if (!back) throw new ApiError(400, "unexpected return URL", { code: "provider_error" });
  if ("error" in back) {
    if (back.error === "denied") return null;
    throw new ApiError(400, back.error, { code: back.error });
  }
  return client.oauthExchange(back.code, verifier);
}

type Apple = Pick<typeof AppleAuthentication, "signInAsync">;

/**
 * Sign in with Apple, natively (iOS 13+). Apple gets the SHA-256 of a fresh nonce and echoes it in the
 * identity token; the server checks it against the raw nonce sent here, so a token cannot be replayed.
 * Resolves null when the person cancels the Apple sheet.
 */
export async function signInWithAppleNative(client: HosbahClient, locale: string, apple: Apple = AppleAuthentication): Promise<LoginResult | null> {
  const nonce = randomHex(32);
  let cred: AppleAuthentication.AppleAuthenticationCredential;
  try {
    cred = await apple.signInAsync({
      requestedScopes: [AppleAuthentication.AppleAuthenticationScope.FULL_NAME, AppleAuthentication.AppleAuthenticationScope.EMAIL],
      nonce: await sha256Hex(nonce),
    });
  } catch (e) {
    if ((e as { code?: string }).code === "ERR_REQUEST_CANCELED") return null;
    throw e;
  }
  if (!cred.identityToken || !cred.authorizationCode) throw new ApiError(400, "Apple returned no identity token", { code: "provider_error" });
  return client.appleNative({ id_token: cred.identityToken, authorization_code: cred.authorizationCode, nonce, locale });
}

/** Native Sign in with Apple is there on iOS 13+ only; everywhere else Apple goes through the browser. */
export function appleNativeAvailable(): Promise<boolean> {
  return AppleAuthentication.isAvailableAsync().catch(() => false);
}
