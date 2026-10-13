import { ApiError } from "@hosbah/client";
import { SecurityLevel } from "expo-local-authentication";

import { authenticateForAction, type Authenticator } from "@/lib/confirm";
import { errorKey, testPushErrorKey } from "@/lib/errors";

const fake = (level: SecurityLevel, success = true) => {
  const authenticateAsync = jest.fn().mockResolvedValue({ success });
  const auth = { getEnrolledLevelAsync: jest.fn().mockResolvedValue(level), authenticateAsync } as unknown as Authenticator;
  return { auth, authenticateAsync };
};

describe("authenticateForAction", () => {
  it("skips the prompt when nothing is enrolled", async () => {
    const { auth, authenticateAsync } = fake(SecurityLevel.NONE);
    expect(await authenticateForAction("r", "c", auth)).toBe(true);
    expect(authenticateAsync).not.toHaveBeenCalled();
  });
  it("prompts with device fallback allowed when biometrics or a passcode exist", async () => {
    const { auth, authenticateAsync } = fake(SecurityLevel.BIOMETRIC_STRONG);
    expect(await authenticateForAction("Redeploy", "Cancel", auth)).toBe(true);
    expect(authenticateAsync).toHaveBeenCalledWith(expect.objectContaining({ promptMessage: "Redeploy", disableDeviceFallback: false }));
  });
  it("returns false when the user fails or cancels", async () => {
    expect(await authenticateForAction("r", "c", fake(SecurityLevel.SECRET, false).auth)).toBe(false);
  });
});

describe("errorKey", () => {
  it("maps statuses and codes", () => {
    expect(errorKey(new ApiError(401, "x"))).toBe("error.invalid_credentials");
    expect(errorKey(new ApiError(403, "x"))).toBe("error.forbidden");
    expect(errorKey(new ApiError(404, "x"))).toBe("error.not_found");
    expect(errorKey(new ApiError(503, "x"))).toBe("error.push_disabled");
    expect(errorKey(new ApiError(401, "x", { code: "2fa_invalid" }))).toBe("error.2fa_invalid");
    expect(errorKey(new ApiError(500, "x"))).toBe("error.generic");
    expect(errorKey(new Error("boom"))).toBe("error.generic");
  });
  it("has a specific message for a device without a push token", () => {
    expect(testPushErrorKey(new ApiError(409, "x"))).toBe("settings.testPushNoToken");
    expect(testPushErrorKey(new ApiError(503, "x"))).toBe("error.push_disabled");
  });
});
