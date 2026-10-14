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
    case "provider_error":
    case "provider_unavailable":
    case "oauth_email_unverified":
    case "signup_closed":
    case "invite_only":
    case "state_invalid":
    case "rate_limited":
    case "invalid_password":
    case "sole_owner":
    case "team_has_servers":
    case "last_admin":
      return `error.${err.code}`;
    case "denied":
      return "login.socialCancelled";
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
