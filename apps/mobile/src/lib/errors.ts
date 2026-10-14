import { ApiError } from "@hosbah/client";
import type { MessageKey } from "@hosbah/client/i18n";

/** Maps whatever a call threw to a catalog key, so every screen shows the same wording in either language. */
export function errorKey(err: unknown): MessageKey {
  if (!(err instanceof ApiError)) return "error.generic";
  if (err.isNetwork) return "error.network";
  switch (err.code) {
    case "2fa_invalid":
    case "invalid_totp":
    case "invalid_code":
      return "error.2fa_invalid";
    case "account_disabled":
      return "error.account_disabled";
    case "email_unverified":
      return "error.email_unverified";
    case "invalid_password":
      return "error.invalid_password";
    case "rate_limited":
      return "error.rate_limited";
    case "signup_closed":
      return "error.signup_closed";
    case "invite_only":
      return "error.invite_only";
    case "oauth_email_unverified":
      return "error.oauth_email_unverified";
    case "identity_taken":
      return "error.identity_taken";
    case "provider_unavailable":
      return "error.provider_unavailable";
    case "provider_error":
    case "state_invalid":
    case "internal":
      return "error.oauth";
    case "confirm_mismatch":
      return "error.confirm_mismatch";
    case "invalid_credentials":
      // Account deletion answers 422 for a wrong password; a rejected login is a 401.
      return err.status === 422 ? "error.wrong_password" : "error.invalid_credentials";
  }
  switch (err.status) {
    case 0:
      return "error.network";
    case 401:
      // A rejected login is 401 too; a dead session is reported separately via onAuthRequired.
      return "error.invalid_credentials";
    case 403:
      return "error.forbidden";
    case 404:
      return "error.not_found";
    case 429:
      return "error.rate_limited";
    case 503:
      return "error.push_disabled";
    default:
      return "error.generic";
  }
}

/** Same, for the test-notification call, where 409 (no push token yet) and 503 (server has no FCM) mean something specific. */
export function testPushErrorKey(err: unknown): MessageKey {
  if (err instanceof ApiError && err.status === 409) return "settings.testPushNoToken";
  return errorKey(err);
}
