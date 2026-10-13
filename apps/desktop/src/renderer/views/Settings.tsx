import { useState } from "react";
import { EVENT_NAMES, normalizeApiUrl, type EventName, type NotificationPrefs } from "@hosbah/client";
import { Button, Card, ErrorNote, PageHeader, Segmented, Spinner, Toggle } from "../components/ui";
import { useApp } from "../lib/app";
import { BridgeError, call, data } from "../lib/bridge";
import { useData } from "../lib/useData";

export function Settings() {
  const { t, locale, prefs, setPrefs, session, info, refreshSession } = useApp();
  const [url, setUrl] = useState(prefs.apiUrl);
  const [urlError, setUrlError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const np = useData(() => data<NotificationPrefs>("prefs"), []);

  async function toggle(ev: EventName, on: boolean) {
    const base = np.data ?? ({} as NotificationPrefs);
    try {
      await data("putPrefs", { ...base, [ev]: on });
    } finally {
      np.reload();
    }
  }

  async function saveUrl() {
    setUrlError(null);
    let next: string;
    try {
      next = normalizeApiUrl(url);
    } catch {
      setUrlError(t("settings.serverUrl.invalid"));
      return;
    }
    if (next === prefs.apiUrl) return;
    await setPrefs({ apiUrl: next });
    setUrl(next);
    // A different server means a different account: the session here is no longer valid.
    await call("session:signOut");
    await refreshSession();
  }

  async function signOut() {
    await call("session:signOut");
    await refreshSession();
  }

  return (
    <>
      <PageHeader title={t("tab.settings")} sub={session?.email ? t("nav.signedInAs", { email: session.email }) : undefined} />
      <div className="space-y-5">
        <Card className="divide-y divide-line px-5">
          <div className="flex items-center justify-between gap-4 py-3">
            <span className="text-sm font-medium text-fg">{t("settings.language")}</span>
            <Segmented
              label={t("settings.language")}
              value={locale}
              options={[
                { value: "ar", label: t("settings.language.ar") },
                { value: "en", label: t("settings.language.en") },
              ]}
              onChange={(v) => void setPrefs({ locale: v })}
            />
          </div>
          <div className="flex items-center justify-between gap-4 py-3">
            <span className="text-sm font-medium text-fg">{t("settings.theme")}</span>
            <Segmented
              label={t("settings.theme")}
              value={prefs.theme}
              options={[
                { value: "system", label: t("settings.theme.system") },
                { value: "light", label: t("settings.theme.light") },
                { value: "dark", label: t("settings.theme.dark") },
              ]}
              onChange={(v) => void setPrefs({ theme: v })}
            />
          </div>
          <Toggle checked={prefs.launchAtLogin} onChange={(v) => void setPrefs({ launchAtLogin: v })} label={t("settings.startAtLogin")} />
        </Card>

        <Card className="p-5">
          <h2 className="text-sm font-semibold text-fg">{t("settings.notifications")}</h2>
          <p className="mb-2 text-xs text-muted">{t("settings.notifications.desc")}</p>
          {np.error && <ErrorNote error={np.error} onRetry={np.reload} />}
          {!np.data && !np.error ? (
            <Spinner />
          ) : (
            <div className="divide-y divide-line">
              {EVENT_NAMES.map((ev) => (
                <Toggle key={ev} checked={np.data?.[ev] !== false} onChange={(v) => void toggle(ev, v)} label={t(`event.${ev}` as never)} desc={t(`event.${ev}.desc` as never)} />
              ))}
            </div>
          )}
        </Card>

        <Card className="p-5">
          <h2 className="text-sm font-semibold text-fg">{t("settings.serverUrl")}</h2>
          <p className="mb-3 text-xs text-muted">{t("settings.serverUrl.hint")}</p>
          <div className="flex gap-2">
            <input
              className="ltr min-w-0 flex-1 rounded-lg border border-line bg-elevated px-3 py-2 text-sm text-fg"
              value={url}
              onChange={(e) => {
                setUrl(e.target.value);
                setSaved(false);
              }}
              aria-label={t("settings.serverUrl")}
            />
            <Button
              onClick={() =>
                void saveUrl()
                  .then(() => setSaved(true))
                  .catch((e: unknown) => setUrlError(e instanceof BridgeError ? e.message : t("error.generic")))
              }
            >
              {t("settings.serverUrl.save")}
            </Button>
          </div>
          {urlError && <p className="mt-2 text-sm text-danger-text">{urlError}</p>}
          {saved && !urlError && <p className="mt-2 text-sm text-ok-text">{t("settings.saved")}</p>}
        </Card>

        <Card className="flex flex-wrap items-center justify-between gap-3 p-5">
          <div>
            <h2 className="text-sm font-semibold text-fg">{t("settings.account")}</h2>
            <p className="ltr text-xs text-muted">{session?.email}</p>
            <p className="mt-1 text-xs text-muted">
              {t("settings.version", { version: info?.version ?? "" })}
              {info && !info.encrypted ? " · !" : ""}
            </p>
          </div>
          <Button variant="danger" onClick={() => void signOut()}>
            {t("settings.signOut")}
          </Button>
        </Card>
      </div>
    </>
  );
}
