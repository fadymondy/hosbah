// IPC handlers. Every reply is a Reply<T> so a failure keeps its status and code across the bridge.
import { app, ipcMain, nativeTheme } from "electron";
import { ApiError, type Locale, type NotificationPrefsResponse } from "@hosbah/client";
import { DATA_FNS, type ActionRequest, type AppInfo, type DataFn, type Reply } from "../core/ipc";
import { tr } from "../core/strings";
import * as confirm from "./confirm";
import * as monitor from "./monitor";
import * as session from "./session";
import * as store from "./store";

const wrap =
  <A extends unknown[], T>(fn: (...a: A) => Promise<T> | T) =>
  async (_e: unknown, ...a: A): Promise<Reply<T>> => {
    try {
      return { ok: true, data: await fn(...a) };
    } catch (e) {
      if (e instanceof ApiError) return { ok: false, status: e.status, ...(e.code ? { code: e.code } : {}), message: e.message };
      return { ok: false, status: 0, message: e instanceof Error ? e.message : String(e) };
    }
  };

export function applyPrefs(p: store.Prefs): void {
  nativeTheme.themeSource = p.theme;
  if (process.env.HOSBAH_E2E !== "1") app.setLoginItemSettings({ openAtLogin: p.launchAtLogin });
}

export function appInfo(): AppInfo {
  return {
    version: app.getVersion(),
    platform: session.platform() as AppInfo["platform"],
    touchId: confirm.touchIdAvailable(),
    e2e: process.env.HOSBAH_E2E === "1",
    encrypted: store.canEncrypt(),
    locale: store.loadPrefs().locale,
  };
}

export function register(broadcast: (channel: string, payload: unknown) => void): void {
  session.events.on("session", (i) => broadcast("session", i));
  monitor.events.on("change", (s) => broadcast("monitor", s));

  ipcMain.handle("session:info", wrap(() => session.info()));
  ipcMain.handle(
    "session:signIn",
    wrap(async (apiUrl: string, email: string, password: string) => {
      const r = await session.signIn(apiUrl, email, password, store.loadPrefs().locale);
      if (r.ok) onSignedIn();
      return r;
    }),
  );
  ipcMain.handle(
    "session:twoFactor",
    wrap(async (challenge: string, answer: { code?: string; recovery_code?: string }) => {
      const r = await session.submitTwoFactor(challenge, answer, store.loadPrefs().locale);
      if (r.ok) onSignedIn();
      return r;
    }),
  );
  ipcMain.handle(
    "session:signOut",
    wrap(async () => {
      monitor.stop();
      await session.signOut();
    }),
  );
  ipcMain.handle(
    "session:setTeam",
    wrap((slug: string) => {
      session.setActiveTeam(slug);
    }),
  );

  // Reads: only the allowlisted client methods, with the arguments the renderer gives.
  ipcMain.handle(
    "data",
    wrap(async (fn: DataFn, args: unknown[] = []) => {
      const c = session.api();
      if (!c) throw new ApiError(401, "sign in required");
      if (!(DATA_FNS as readonly string[]).includes(fn)) throw new Error(`blocked call: ${fn}`);
      const out = await (c[fn] as (...a: unknown[]) => Promise<unknown>).apply(c, args);
      if (fn === "putPrefs" && out && typeof out === "object") monitor.setPrefs((out as NotificationPrefsResponse).events);
      return out;
    }),
  );

  // The one write path. The role check is the server's job too (403), this is the extra local gate.
  ipcMain.handle(
    "action",
    wrap(async (req: ActionRequest) => {
      const c = session.api();
      if (!c) throw new ApiError(401, "sign in required");
      const team = session.info().teams.find((t) => t.slug === req.team);
      if (!team || !["owner", "admin", "deployer"].includes(team.role)) throw new ApiError(403, "your role cannot change apps");
      const prefs = store.loadPrefs();
      const label = tr(prefs.locale, `action.${req.action}` as never);
      if (!(await confirm.authorize(`${label}: ${req.appName}`))) throw new ApiError(0, "cancelled", { code: "cancelled" });
      const r =
        req.action === "redeploy"
          ? await c.redeploy(req.team, req.app)
          : req.action === "restart"
            ? await c.restart(req.team, req.app)
            : await c.power(req.team, req.app, req.action);
      void monitor.refreshTeam(req.team);
      return r;
    }),
  );

  ipcMain.handle(
    "alerts:markRead",
    wrap(async (team: string, id: string) => {
      const c = session.api();
      if (!c) throw new ApiError(401, "sign in required");
      if (id === "all") await c.markAllRead(team);
      else await c.markRead(team, id);
      monitor.markLocal(team, id);
    }),
  );

  ipcMain.handle("prefs:get", wrap(() => store.loadPrefs()));
  ipcMain.handle(
    "prefs:set",
    wrap((patch: Partial<store.Prefs>) => {
      const cur = store.loadPrefs();
      const next: store.Prefs = {
        theme: patch.theme ?? cur.theme,
        locale: (patch.locale as Locale | undefined) ?? cur.locale,
        apiUrl: patch.apiUrl ?? cur.apiUrl,
        launchAtLogin: patch.launchAtLogin ?? cur.launchAtLogin,
      };
      store.savePrefs(next);
      applyPrefs(next);
      broadcast("prefs", next);
      broadcast("monitor", monitor.state()); // the tray text follows the language
      return next;
    }),
  );

  ipcMain.handle("monitor:state", wrap(() => monitor.state()));
  ipcMain.handle("app:info", wrap(() => appInfo()));
}

function onSignedIn(): void {
  void monitor.loadPrefs();
  monitor.sync();
}
