import type { AuthProvider } from "@hosbah/client";

jest.mock("expo-apple-authentication", () => ({ AppleAuthenticationScope: { FULL_NAME: 0, EMAIL: 1 } }));
jest.mock("expo-web-browser", () => ({ openAuthSessionAsync: jest.fn() }));
jest.mock("expo-crypto", () => ({}));
jest.mock("@react-native-google-signin/google-signin", () => ({ GoogleSignin: {} }));

// eslint-disable-next-line @typescript-eslint/no-require-imports
const { base64url, methodFor, parseCallback, socialButtons } = require("@/lib/social") as typeof import("@/lib/social");
import { errorKey } from "@/lib/errors";
import { ApiError } from "@hosbah/client";

const all: AuthProvider[] = [
  { id: "google", name: "Google", web: true, native: true, client_id: "web.apps.googleusercontent.com" },
  { id: "github", name: "GitHub", web: true, native: false },
  { id: "apple", name: "Apple", web: true, native: true },
];

describe("social sign-in", () => {
  it("puts Apple first (guideline 4.8), then Google, then GitHub", () => {
    expect(socialButtons(all, "android").map((p) => p.id)).toEqual(["apple", "google", "github"]);
  });

  it("hides what the server has not set up", () => {
    const bare: AuthProvider[] = [
      { id: "google", name: "Google", web: true, native: false },
      { id: "apple", name: "Apple", web: false, native: true },
    ];
    expect(socialButtons(bare, "android")).toEqual([]);
    expect(socialButtons([], "ios")).toEqual([]);
  });

  it("never shows Google or GitHub on iOS without Sign in with Apple (guideline 4.8)", () => {
    const noApple = all.filter((p) => p.id !== "apple");
    expect(socialButtons(noApple, "ios")).toEqual([]);
    expect(socialButtons(all, "ios", false)).toEqual([]);
    expect(socialButtons(all, "ios").map((p) => p.id)).toEqual(["apple", "github"]);
    expect(socialButtons(noApple, "android").map((p) => p.id)).toEqual(["google", "github"]);
  });

  it("uses the iOS sheet for Apple on iOS and the browser on Android", () => {
    const apple = all[2]!;
    expect(methodFor(apple, "ios", true)).toBe("native");
    expect(methodFor(apple, "ios", false)).toBeNull();
    expect(methodFor(apple, "android")).toBe("browser");
    expect(methodFor(all[1]!, "ios")).toBe("browser");
  });

  it("hides Google on iOS in a build without the iOS client id", () => {
    expect(methodFor(all[0]!, "ios")).toBeNull();
    expect(methodFor(all[0]!, "android")).toBe("native");
  });

  it("reads the deep link the browser trip ends on", () => {
    expect(parseCallback("hosbah://auth/callback?code=abc")).toEqual({ code: "abc", error: undefined });
    expect(parseCallback("hosbah://auth/callback?error=signup_closed")).toEqual({ code: undefined, error: "signup_closed" });
    expect(base64url("ab+/cd==")).toBe("ab-_cd");
  });

  it("maps the server's refusals to messages", () => {
    expect(errorKey(new ApiError(403, "x", { code: "signup_closed" }))).toBe("error.signup_closed");
    expect(errorKey(new ApiError(409, "x", { code: "sole_owner" }))).toBe("error.sole_owner");
    expect(errorKey(new ApiError(403, "x", { code: "invalid_password" }))).toBe("error.invalid_password");
  });
});
