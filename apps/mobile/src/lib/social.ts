import type { AuthProvider, AuthProviderId, HosbahClient, LoginResult, NativeSignIn } from "@hosbah/client";
import { ApiError } from "@hosbah/client";
import * as AppleAuthentication from "expo-apple-authentication";
import * as Crypto from "expo-crypto";
import * as WebBrowser from "expo-web-browser";
import { Platform } from "react-native";

/*
Social sign-in in the app (server: internal/account/native.go).

  - Apple on iOS: the system sheet (expo-apple-authentication). Apple gets the SHA-256 of a random
    nonce, the server gets the raw one and checks the identity token carries its hash.
  - Google: the Google Sign-In SDK (@react-native-google-signin/google-signin) mints an id_token for
    the server's web client id (GET /api/auth/providers → client_id) — on iOS through the iOS client
    (EXPO_PUBLIC_GOOGLE_IOS_CLIENT_ID, whose reversed id is the URL scheme baked in by app.config.js).
  - GitHub, and Apple on Android: the server's own round trip in the system browser
    (openAuthSessionAsync), with this app's PKCE pair; it ends at hosbah://auth/callback?code=… and
    the code plus the verifier buy the session (POST /api/auth/app/token).

Every path ends in a LoginResult or an ApiError exactly like a password sign-in (2fa_required etc.).
A person backing out throws SocialCancelled, which the screen ignores.
*/

export const APP_REDIRECT = "hosbah://auth/callback";
const GOOGLE_IOS_CLIENT_ID = process.env.EXPO_PUBLIC_GOOGLE_IOS_CLIENT_ID || "";

export class SocialCancelled extends Error {
  constructor() {
    super("cancelled");
    this.name = "SocialCancelled";
  }
}

/** How the app signs in with a provider on this platform, or null when it cannot. */
export type SocialMethod = "native" | "browser";

export function methodFor(p: AuthProvider, os: string = Platform.OS, appleAvailable = true): SocialMethod | null {
  switch (p.id) {
    case "apple":
      if (os === "ios") return p.native && appleAvailable ? "native" : null;
      return p.web ? "browser" : null;
    case "google":
      // The SDK needs the server's web client id, and on iOS the iOS client this build was made with.
      if (!p.native || !p.client_id) return null;
      if (os === "ios" && !GOOGLE_IOS_CLIENT_ID) return null;
      return googleModule() ? "native" : null;
    case "github":
      return p.web ? "browser" : null;
  }
  return null;
}

/**
 * The buttons to show, Apple first (App Store guideline 4.8: Sign in with Apple at least as prominent as
 * the other providers), then Google, then GitHub.
 */
export function socialButtons(providers: AuthProvider[], os: string = Platform.OS, appleAvailable = true): AuthProvider[] {
  const order: AuthProviderId[] = ["apple", "google", "github"];
  const shown = order
    .map((id) => providers.find((p) => p.id === id))
    .filter((p): p is AuthProvider => !!p && methodFor(p, os, appleAvailable) !== null);
  // Guideline 4.8: on iOS a third-party login (Google, GitHub) may only appear next to Sign in with Apple.
  // A server without the Apple keys (or a device without the Apple sheet) gets no social buttons on iOS.
  if (os === "ios" && !shown.some((p) => p.id === "apple")) return [];
  return shown;
}

export function base64url(b64: string): string {
  return b64.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

async function pkcePair(): Promise<{ verifier: string; challenge: string }> {
  const bytes = Crypto.getRandomBytes(32);
  let bin = "";
  bytes.forEach((b) => (bin += String.fromCharCode(b)));
  const verifier = base64url(btoa(bin));
  const digest = await Crypto.digestStringAsync(Crypto.CryptoDigestAlgorithm.SHA256, verifier, { encoding: Crypto.CryptoEncoding.BASE64 });
  return { verifier, challenge: base64url(digest) };
}

/** Parses the deep link the browser trip ended on. */
export function parseCallback(url: string): { code?: string; error?: string } {
  const q = url.includes("?") ? url.slice(url.indexOf("?") + 1).split("#")[0] : "";
  const params = new URLSearchParams(q);
  return { code: params.get("code") ?? undefined, error: params.get("error") ?? undefined };
}

async function viaBrowser(client: HosbahClient, provider: AuthProviderId, locale: string): Promise<LoginResult> {
  const { verifier, challenge } = await pkcePair();
  const result = await WebBrowser.openAuthSessionAsync(client.oauthBrowserUrl(provider, challenge, locale), APP_REDIRECT, {
    preferEphemeralSession: false,
  });
  if (result.type !== "success") throw new SocialCancelled();
  const { code, error } = parseCallback(result.url);
  if (error) throw new ApiError(error === "denied" ? 400 : 403, error, { code: error });
  if (!code) throw new ApiError(400, "no code", { code: "provider_error" });
  return client.exchangeAppCode(code, verifier);
}

/** What Sign in with Apple on iOS returned, for the server (sign-in or deletion proof). */
export async function appleCredential(): Promise<NativeSignIn> {
  const raw = Crypto.randomUUID();
  const hashed = await Crypto.digestStringAsync(Crypto.CryptoDigestAlgorithm.SHA256, raw);
  try {
    const c = await AppleAuthentication.signInAsync({
      requestedScopes: [AppleAuthentication.AppleAuthenticationScope.FULL_NAME, AppleAuthentication.AppleAuthenticationScope.EMAIL],
      nonce: hashed,
    });
    if (!c.identityToken || !c.authorizationCode) throw new ApiError(400, "no identity token", { code: "provider_error" });
    const given = c.fullName?.givenName ?? undefined;
    const family = c.fullName?.familyName ?? undefined;
    return {
      id_token: c.identityToken,
      authorization_code: c.authorizationCode,
      nonce: raw,
      ...(given || family ? { full_name: { given_name: given, family_name: family } } : {}),
    };
  } catch (e) {
    if ((e as { code?: string }).code === "ERR_REQUEST_CANCELED") throw new SocialCancelled();
    throw e;
  }
}

type GoogleModule = typeof import("@react-native-google-signin/google-signin");
let google: GoogleModule | null | undefined;
/** The Google SDK, or null in a build without its native module (Expo Go, tests). */
function googleModule(): GoogleModule | null {
  if (google === undefined) {
    try {
      // eslint-disable-next-line @typescript-eslint/no-require-imports
      google = require("@react-native-google-signin/google-signin") as GoogleModule;
    } catch {
      google = null;
    }
  }
  return google;
}

/** What Google Sign-In returned, for the server (sign-in or deletion proof). */
export async function googleCredential(webClientId: string): Promise<NativeSignIn> {
  const g = googleModule();
  if (!g) throw new ApiError(404, "unavailable", { code: "provider_unavailable" });
  const { GoogleSignin, isCancelledResponse, isErrorWithCode, statusCodes } = g;
  GoogleSignin.configure({ webClientId, ...(GOOGLE_IOS_CLIENT_ID ? { iosClientId: GOOGLE_IOS_CLIENT_ID } : {}) });
  try {
    if (Platform.OS === "android") await GoogleSignin.hasPlayServices({ showPlayServicesUpdateDialog: true });
    const r = await GoogleSignin.signIn();
    if (isCancelledResponse(r)) throw new SocialCancelled();
    if (!r.data.idToken) throw new ApiError(400, "no id token", { code: "provider_error" });
    return { id_token: r.data.idToken };
  } catch (e) {
    if (e instanceof SocialCancelled || e instanceof ApiError) throw e;
    if (isErrorWithCode(e) && (e.code === statusCodes.SIGN_IN_CANCELLED || e.code === statusCodes.IN_PROGRESS)) throw new SocialCancelled();
    throw new ApiError(400, "google sign-in failed", { code: "provider_error" });
  } finally {
    // The next sign-in shows the account picker again rather than reusing this choice silently.
    void GoogleSignin.signOut().catch(() => undefined);
  }
}

/** Signs in with provider p; resolves like client.login(). */
export async function socialSignIn(client: HosbahClient, p: AuthProvider, locale: string): Promise<LoginResult> {
  const method = methodFor(p);
  if (method === "browser") return viaBrowser(client, p.id, locale);
  if (p.id === "apple") return client.nativeSignIn("apple", await appleCredential());
  if (p.id === "google" && p.client_id) return client.nativeSignIn("google", await googleCredential(p.client_id));
  throw new ApiError(404, "unavailable", { code: "provider_unavailable" });
}

/** Whether the system Sign in with Apple sheet exists here (iOS 13+). */
export async function appleAvailable(): Promise<boolean> {
  if (Platform.OS !== "ios") return false;
  try {
    return await AppleAuthentication.isAvailableAsync();
  } catch {
    return false;
  }
}
