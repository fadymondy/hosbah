"use client"

import { apiFetch, ApiError } from "@/lib/api"

/*
The account cycle, on the API's /api/auth/* and /api/me/2fa/* (the same flow as fadymondy.com):
a new account is unusable until the 6-digit code mailed to it is entered; a reset code sets a new
password and signs every other session out; an emailed code can replace the password; and an
account with an authenticator needs its code (or a recovery code) as a second step.
The server decides everything; these calls only walk the visitor through it.
*/

export const MIN_PASSWORD = 8

type Session = { token?: string; user?: { id: string; email: string } }

/** What a sign-in attempt needs next, read off the API's refusal. */
export type Next =
  | { kind: "verify"; email: string } // email_unverified: enter the mailed code first
  | { kind: "challenge"; challenge: string } // 2fa_required: authenticator or recovery code

/** A refusal that moves the visitor to another step instead of showing an error. */
export function nextStep(err: unknown, email: string): Next | null {
  if (!(err instanceof ApiError)) return null
  if (err.reason === "email_unverified") {
    return { kind: "verify", email: typeof err.data.email === "string" ? err.data.email : email }
  }
  if (err.reason === "2fa_required" && typeof err.data.challenge === "string") {
    return { kind: "challenge", challenge: err.data.challenge }
  }
  return null
}

const post = <T>(path: string, body: unknown) => apiFetch<T>(path, { method: "POST", body })

export const auth = {
  login: (email: string, password: string, locale: string) => post<Session>("/api/auth/login", { email, password, locale }),
  register: (email: string, password: string, locale: string, invitation_token?: string) =>
    post<Session>("/api/auth/register", { email, password, locale, ...(invitation_token ? { invitation_token } : {}) }),
  verifyEmail: (email: string, code: string) => post<Session & { status?: string }>("/api/auth/verify-email", { email, code }),
  resendVerification: (email: string, locale: string) => post<{ status: string }>("/api/auth/verify-email/resend", { email, locale }),
  forgotPassword: (email: string, locale: string) => post<{ status: string }>("/api/auth/password/forgot", { email, locale }),
  resetPassword: (email: string, code: string, password: string) =>
    post<{ status: string }>("/api/auth/password/reset", { email, code, password }),
  requestCode: (email: string, locale: string) => post<{ status: string }>("/api/auth/code/request", { email, locale }),
  signInWithCode: (email: string, code: string) => post<Session>("/api/auth/code/verify", { email, code }),
  challenge: (challenge: string, answer: { code?: string; recovery_code?: string }) =>
    post<Session & { extra?: { recovery_codes_left?: number } }>("/api/auth/2fa/challenge", { challenge, ...answer }),
  changePassword: (oldPassword: string, newPassword: string) =>
    post<{ status: string }>("/api/auth/change-password", { old_password: oldPassword, new_password: newPassword }),
  twoFactor: {
    status: () => apiFetch<{ enabled: boolean; recovery_codes_left: number; available: boolean }>("/api/me/2fa"),
    enroll: () => post<{ secret: string; otpauth_url: string; qr: string }>("/api/me/2fa/enroll", {}),
    confirm: (code: string) => post<{ enabled: boolean; recovery_codes: string[] }>("/api/me/2fa/confirm", { code }),
    disable: (answer: { code?: string; recovery_code?: string }) => post<{ enabled: boolean }>("/api/me/2fa/disable", answer),
    newRecoveryCodes: (code: string) => post<{ recovery_codes: string[] }>("/api/me/2fa/recovery", { code }),
  },
}

/** The API's refusals a visitor can meet in these forms, shown in the page's language. */
export function authMessage(err: unknown, t: (key: string) => string, fallback: (err: unknown) => string): string {
  if (err instanceof ApiError) {
    if (err.status === 429) return t("account.tooMany")
    switch (err.reason) {
      case "invalid_code":
        return t("account.codeInvalid")
      case "rate_limited":
        return t("account.tooMany")
      case "2fa_invalid":
        return t("account.codeWrong")
      case "challenge_expired":
        return t("account.challengeExpired")
      case "account_disabled":
        return t("account.disabled")
    }
  }
  return fallback(err)
}
