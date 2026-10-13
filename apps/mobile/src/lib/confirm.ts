import * as LocalAuthentication from "expo-local-authentication";

export type Authenticator = Pick<typeof LocalAuthentication, "getEnrolledLevelAsync" | "authenticateAsync">;

/**
 * The second factor for destructive actions, after the on-screen confirm dialog.
 *
 * - Something is enrolled (biometrics, or just a device passcode): ask for it; the OS falls back to the
 *   passcode when biometrics fail or are unavailable. Returns whether the user passed.
 * - Nothing is enrolled (no lock screen at all): there is nothing to ask. The dialog already confirmed
 *   the action, so this resolves true rather than locking the user out of their own servers.
 */
export async function authenticateForAction(
  reason: string,
  cancelLabel: string,
  auth: Authenticator = LocalAuthentication,
): Promise<boolean> {
  let level: LocalAuthentication.SecurityLevel;
  try {
    level = await auth.getEnrolledLevelAsync();
  } catch {
    return true;
  }
  if (level === LocalAuthentication.SecurityLevel.NONE) return true;
  const r = await auth.authenticateAsync({
    promptMessage: reason,
    cancelLabel,
    disableDeviceFallback: false,
  });
  return r.success;
}
