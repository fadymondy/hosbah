import { createHash } from "crypto";
import { ApiError, type HosbahClient } from "@hosbah/client";

jest.mock("expo-crypto", () => ({
  CryptoDigestAlgorithm: { SHA256: "SHA-256" },
  CryptoEncoding: { HEX: "hex" },
  getRandomBytes: (n: number) => new Uint8Array(require("crypto").randomBytes(n)),
  digestStringAsync: async (_a: string, s: string) => require("crypto").createHash("sha256").update(s).digest("hex"),
}));
jest.mock("expo-web-browser", () => ({ openAuthSessionAsync: jest.fn() }));
jest.mock("expo-apple-authentication", () => ({
  AppleAuthenticationScope: { FULL_NAME: 0, EMAIL: 1 },
  signInAsync: jest.fn(),
  isAvailableAsync: jest.fn(async () => true),
}));

import { providerButtons } from "@/lib/auth-options";
import { errorKey } from "@/lib/errors";
import { isOAuthReturn, OAUTH_RETURN, parseOAuthReturn, signInWithAppleNative, signInWithBrowser } from "@/lib/oauth";
import { redirectSystemPath } from "@/app/+native-intent";

const sha = (s: string) => createHash("sha256").update(s).digest("hex");

function fakeClient() {
  return {
    oauthStartUrl: jest.fn((p: string, ch: string, l: string) => `https://h.example/api/auth/oauth/${p}?app=1&app_challenge=${ch}&locale=${l}`),
    oauthExchange: jest.fn(async () => ({ token: "t", user: { id: "u", email: "e" } })),
    appleNative: jest.fn(async () => ({ token: "t", user: { id: "u", email: "e" } })),
  };
}
const asClient = (c: ReturnType<typeof fakeClient>) => c as unknown as HosbahClient;

describe("the hosbah://oauth return", () => {
  it("is recognised in every shape the OS hands it over", () => {
    expect(isOAuthReturn("hosbah://oauth?code=abc")).toBe(true);
    expect(isOAuthReturn("/oauth?code=abc")).toBe(true);
    expect(isOAuthReturn("hosbah://oauth")).toBe(true);
    expect(isOAuthReturn("hosbah://oauthx")).toBe(false);
    expect(isOAuthReturn("hosbah://t/acme/apps/1")).toBe(false);
  });
  it("carries a code or an error", () => {
    expect(parseOAuthReturn("hosbah://oauth?code=a%2Bb&x=1")).toEqual({ code: "a+b" });
    expect(parseOAuthReturn("hosbah://oauth?error=signup_closed")).toEqual({ error: "signup_closed" });
    expect(parseOAuthReturn("hosbah://oauth")).toEqual({ error: "provider_error" });
    expect(parseOAuthReturn("https://other")).toBeNull();
  });
  it("never reaches the router as an unmatched route", () => {
    expect(redirectSystemPath({ path: "hosbah://oauth?code=abc", initial: false })).toBe("/");
    expect(redirectSystemPath({ path: "/oauth?error=denied", initial: true })).toBe("/");
  });
});

describe("signInWithBrowser", () => {
  it("sends only the verifier's hash out and trades the code with the verifier", async () => {
    const c = fakeClient();
    let started = "";
    const browser = {
      openAuthSessionAsync: jest.fn(async (url: string, ret?: string | null) => {
        started = url;
        expect(ret).toBe(OAUTH_RETURN);
        return { type: "success" as const, url: "hosbah://oauth?code=one-time" };
      }),
    };
    const r = await signInWithBrowser(asClient(c), "google", "en", browser);
    expect(r?.token).toBe("t");
    const [code, verifier] = c.oauthExchange.mock.calls[0] as unknown as [string, string];
    expect(code).toBe("one-time");
    expect(verifier).toMatch(/^[0-9a-f]{64}$/);
    expect(started).toContain(`app_challenge=${sha(verifier)}`);
    expect(started).not.toContain(verifier);
    expect(c.oauthStartUrl).toHaveBeenCalledWith("google", sha(verifier), "en");
  });
  it("resolves null when the person backs out", async () => {
    const c = fakeClient();
    expect(await signInWithBrowser(asClient(c), "github", "ar", { openAuthSessionAsync: async () => ({ type: "cancel" as const }) })).toBeNull();
    expect(await signInWithBrowser(asClient(c), "github", "ar", { openAuthSessionAsync: async () => ({ type: "success" as const, url: "hosbah://oauth?error=denied" }) })).toBeNull();
    expect(c.oauthExchange).not.toHaveBeenCalled();
  });
  it("throws the server's refusal as an ApiError with its code", async () => {
    const c = fakeClient();
    const err = await signInWithBrowser(asClient(c), "google", "en", {
      openAuthSessionAsync: async () => ({ type: "success" as const, url: "hosbah://oauth?error=oauth_email_unverified" }),
    }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(errorKey(err)).toBe("error.oauth_email_unverified");
  });
});

describe("signInWithAppleNative", () => {
  it("gives Apple the nonce's hash and the server the raw nonce", async () => {
    const c = fakeClient();
    let hashed = "";
    const apple = {
      signInAsync: jest.fn(async (o?: { nonce?: string }) => {
        hashed = o?.nonce ?? "";
        return { identityToken: "id.tok.en", authorizationCode: "ac", user: "s", email: null, fullName: null, realUserStatus: 1, state: null };
      }),
    };
    await signInWithAppleNative(asClient(c), "ar", apple as never);
    const input = (c.appleNative.mock.calls[0] as unknown as [{ id_token: string; nonce: string; authorization_code: string; locale: string }])[0];
    expect(input).toMatchObject({ id_token: "id.tok.en", authorization_code: "ac", locale: "ar" });
    expect(sha(input.nonce)).toBe(hashed);
  });
  it("resolves null when the Apple sheet is cancelled", async () => {
    const c = fakeClient();
    const apple = { signInAsync: async () => Promise.reject(Object.assign(new Error("x"), { code: "ERR_REQUEST_CANCELED" })) };
    expect(await signInWithAppleNative(asClient(c), "en", apple as never)).toBeNull();
    expect(c.appleNative).not.toHaveBeenCalled();
  });
});

describe("providerButtons", () => {
  const all = [
    { id: "apple", name: "Apple", web: true, native: true },
    { id: "google", name: "Google", web: true, native: false },
    { id: "github", name: "GitHub", web: true, native: false },
  ];
  it("shows the native Apple sheet first on iOS", () => {
    expect(providerButtons(all, "ios", true).map((b) => b.id)).toEqual(["apple-native", "google", "github"]);
  });
  it("hides Google and GitHub on iOS when Sign in with Apple cannot be offered (guideline 4.8)", () => {
    expect(providerButtons(all.slice(1), "ios", true)).toEqual([]);
    expect(providerButtons(all, "ios", false)).toEqual([]);
    expect(providerButtons([{ ...all[0], native: false }, all[1]], "ios", true)).toEqual([]);
  });
  it("sends Apple through the browser on Android, and only what the server configured", () => {
    expect(providerButtons(all, "android", false).map((b) => b.id)).toEqual(["apple", "google", "github"]);
    expect(providerButtons([all[1]], "android", false).map((b) => b.id)).toEqual(["google"]);
    expect(providerButtons([], "android", false)).toEqual([]);
  });
});

describe("errorKey for the auth codes", () => {
  it("maps sign-up, provider and deletion refusals", () => {
    expect(errorKey(new ApiError(422, "x", { code: "invalid_password" }))).toBe("error.invalid_password");
    expect(errorKey(new ApiError(429, "x"))).toBe("error.rate_limited");
    expect(errorKey(new ApiError(403, "x", { code: "signup_closed" }))).toBe("error.signup_closed");
    expect(errorKey(new ApiError(403, "x", { code: "invite_only" }))).toBe("error.invite_only");
    expect(errorKey(new ApiError(409, "x", { code: "identity_taken" }))).toBe("error.identity_taken");
    expect(errorKey(new ApiError(400, "x", { code: "state_invalid" }))).toBe("error.oauth");
    expect(errorKey(new ApiError(422, "x", { code: "confirm_mismatch" }))).toBe("error.confirm_mismatch");
    expect(errorKey(new ApiError(422, "x", { code: "invalid_credentials" }))).toBe("error.wrong_password");
    expect(errorKey(new ApiError(401, "x", { code: "invalid_credentials" }))).toBe("error.invalid_credentials");
  });
});
