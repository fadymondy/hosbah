import { ApiError } from "@hosbah/client";
import { en } from "@hosbah/client/i18n";

import { errorKey, passwordErrorKey } from "@/lib/errors";
import { redirectSystemPath } from "@/app/+native-intent";

describe("email/password cycle errors", () => {
  it("reads invalid_password as the length rule where a password is being set", () => {
    const weak = new ApiError(422, "x", { code: "invalid_password" });
    expect(passwordErrorKey(weak)).toBe("signup.passwordRule");
    expect(en["signup.passwordRule"]).toMatch(/8 to 72/);
    // Everywhere else the code keeps its sign-in meaning.
    expect(errorKey(weak)).toBe("error.invalid_password");
  });

  it("passes every other error through unchanged", () => {
    expect(passwordErrorKey(new ApiError(400, "x", { code: "invalid_code" }))).toBe("error.2fa_invalid");
    expect(passwordErrorKey(new ApiError(429, "x", { code: "rate_limited" }))).toBe("error.rate_limited");
    expect(passwordErrorKey(new Error("boom"))).toBe("error.generic");
  });

  it("names an unconfirmed account", () => {
    expect(errorKey(new ApiError(403, "x", { code: "email_unverified" }))).toBe("error.email_unverified");
  });
});

describe("deep-link return", () => {
  it("sends hosbah://auth/callback back to the waiting sign-in screen", () => {
    expect(redirectSystemPath({ path: "hosbah://auth/callback?code=abc", initial: false })).toBe("/sign-in");
    expect(redirectSystemPath({ path: "/auth/callback?error=denied", initial: true })).toBe("/sign-in");
  });
});
