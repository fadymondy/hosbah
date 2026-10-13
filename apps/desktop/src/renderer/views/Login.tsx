import { useState, type FormEvent } from "react";
import { brandName } from "@hosbah/client/brand";
import mark from "../../../../shared/assets/mark.svg";
import type { SignInResult } from "../../main/session";
import { Button, Card } from "../components/ui";
import { useApp } from "../lib/app";
import { BridgeError, call } from "../lib/bridge";

const field = "w-full rounded-lg border border-line bg-elevated px-3 py-2 text-sm text-fg placeholder:text-muted";

export function Login() {
  const { t, locale, prefs, setPrefs, info, refreshSession } = useApp();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [apiUrl, setApiUrl] = useState(prefs.apiUrl);
  const [challenge, setChallenge] = useState<string | null>(null);
  const [useRecovery, setUseRecovery] = useState(false);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fail = (e: unknown) => {
    const be = e instanceof BridgeError ? e : null;
    setError(be?.offline ? t("error.network") : be?.code === "account_disabled" || be?.code === "email_unverified" ? be.message : be && be.status === 401 ? t("login.error") : (be?.message ?? t("error.generic")));
  };

  async function submit(ev: FormEvent) {
    ev.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const r = challenge
        ? await call<SignInResult>("session:twoFactor", challenge, useRecovery ? { recovery_code: code.trim() } : { code: code.trim() })
        : await call<SignInResult>("session:signIn", apiUrl, email, password);
      if (!r.ok) setChallenge(r.challenge);
      else {
        if (apiUrl !== prefs.apiUrl) await setPrefs({ apiUrl });
        await refreshSession();
      }
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="grid h-full place-items-center overflow-auto p-6">
      <Card className="w-full max-w-sm p-7 shadow-xl">
        <div className="mb-6 flex flex-col items-center gap-2 text-center">
          <img src={mark} alt="" className="size-14" />
          <h1 className="text-2xl font-semibold text-fg">{brandName[locale]}</h1>
          <p className="text-sm text-muted">{t("login.subtitle")}</p>
        </div>
        <form onSubmit={submit} className="space-y-3">
          {challenge ? (
            <>
              <p className="text-sm text-body">{t("login.2faHelp")}</p>
              <input
                className={`${field} ltr text-center tracking-widest`}
                autoFocus
                inputMode={useRecovery ? "text" : "numeric"}
                placeholder={useRecovery ? t("login.recoveryCode") : "123456"}
                value={code}
                onChange={(e) => setCode(e.target.value)}
                aria-label={useRecovery ? t("login.recoveryCode") : t("login.2faHelp")}
              />
              <button type="button" className="text-xs text-action underline" onClick={() => setUseRecovery((v) => !v)}>
                {useRecovery ? t("login.useCode") : t("login.recoveryCode")}
              </button>
            </>
          ) : (
            <>
              <label className="block">
                <span className="mb-1 block text-xs text-muted">{t("login.email")}</span>
                <input className={`${field} ltr`} type="email" autoFocus autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required />
              </label>
              <label className="block">
                <span className="mb-1 block text-xs text-muted">{t("login.password")}</span>
                <input className={`${field} ltr`} type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
              </label>
              <details className="text-xs text-muted">
                <summary className="cursor-pointer select-none">{t("login.advanced")}</summary>
                <input className={`${field} ltr mt-2`} value={apiUrl} onChange={(e) => setApiUrl(e.target.value)} aria-label={t("login.advanced")} />
                {info?.e2e && <p className="mt-1">{t("login.localOnly")}</p>}
              </details>
            </>
          )}
          {error && (
            <p role="alert" className="rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger-text">
              {error}
            </p>
          )}
          <Button type="submit" variant="primary" className="w-full" busy={busy}>
            {busy ? t("login.signingIn") : t("login.submit")}
          </Button>
        </form>
      </Card>
    </div>
  );
}
