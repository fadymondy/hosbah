// Screenshot + smoke driver, bundled to dist/shots.cjs by scripts/shots.mjs and run with Electron.
// It boots the real main process in HOSBAH_E2E mode against the local mock API, drives the real
// renderer, and captures with webContents.capturePage() (no browser automation). It also exercises
// the alert path: a notification pushed to the mock must raise a native notification and turn the
// tray red, and a deep link must navigate.
import { app } from "electron";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import "./main";

interface Hooks {
  win(): Electron.BrowserWindow | null;
  raised: { team: string; id: string; title: string; hash: string }[];
  trayLevel(): string | null;
  trayModel(): { level: string; unread: number; recent: unknown[] };
  openHash(h: string): void;
  handleLink(u: string): void;
}

const hooks = () => (globalThis as unknown as { __hosbahTest: Hooks }).__hosbahTest;
const API = process.env.HOSBAH_API_URL ?? "http://127.0.0.1:8719";
const OUT = process.env.HOSBAH_SHOTS_DIR ?? join(process.cwd(), "shots");
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const log = (m: string) => process.stdout.write(`[shots] ${m}\n`);
const results: Record<string, unknown> = {};

const w = () => {
  const win = hooks().win();
  if (!win) throw new Error("no window");
  return win;
};
const ev = <T>(js: string) => w().webContents.executeJavaScript(js, true) as Promise<T>;
const invoke = <T>(ch: string, ...args: unknown[]) => ev<{ ok: boolean; data: T; message?: string }>(`window.hosbah.invoke(${JSON.stringify(ch)}, ...${JSON.stringify(args)})`);

async function waitFor(what: string, test: () => Promise<boolean> | boolean, ms = 10_000): Promise<void> {
  const t0 = Date.now();
  while (Date.now() - t0 < ms) {
    if (await test()) return;
    await sleep(120);
  }
  throw new Error(`timed out waiting for ${what}`);
}

async function shot(name: string): Promise<void> {
  await sleep(450);
  const img = await w().webContents.capturePage();
  for (let i = 0; ; i++) {
    try {
      writeFileSync(join(OUT, `desktop-${name}.png`), img.toPNG());
      break;
    } catch (e) {
      if (i >= 4) throw e;
      await sleep(500);
    }
  }
  log(`saved desktop-${name}.png`);
}

const go = async (hash: string, ready: string) => {
  await ev(`window.location.hash = ${JSON.stringify(hash)}`);
  await waitFor(ready, () => ev<boolean>(`!!document.querySelector(${JSON.stringify(ready)})`));
};

const clickText = (sel: string, text: string) =>
  ev<boolean>(`(() => { const b = [...document.querySelectorAll(${JSON.stringify(sel)})].find(e => e.textContent.includes(${JSON.stringify(text)})); if (b) { b.click(); return true } return false })()`);

async function main() {
  mkdirSync(OUT, { recursive: true });
  const u = new URL(API);
  if (!["127.0.0.1", "localhost"].includes(u.hostname)) throw new Error("refusing to run against a non-local server");
  await waitFor("window", () => !!hooks()?.win());
  await waitFor("renderer", () => ev<boolean>("!!document.querySelector('#root > *')"));

  // ── signed out: sign-in screen, both languages
  for (const [loc, theme] of [["ar", "light"], ["en", "dark"]] as const) {
    await invoke("prefs:set", { locale: loc, theme });
    await shot(`${loc}-${theme}-login`);
  }

  // ── 2FA user: challenge then code
  const first = await invoke<{ ok: boolean; mfa?: boolean; challenge?: string }>("session:signIn", API, "twofa@example.com", "demo-pass");
  results.twoFactorChallenge = first.ok && first.data.ok === false && first.data.mfa === true;
  const second = await invoke<{ ok: boolean }>("session:twoFactor", (first.data as { challenge: string }).challenge, { code: "123456" });
  results.twoFactorSignIn = second.ok && second.data.ok === true;
  await invoke("session:signOut");
  await sleep(300);

  // ── real sign-in
  const r = await invoke<{ ok: boolean }>("session:signIn", API, "demo@example.com", "demo-pass");
  if (!r.ok || !r.data.ok) throw new Error(`sign-in failed: ${r.message}`);
  await waitFor("shell", () => ev<boolean>("!!document.querySelector('nav')"));
  await waitFor("streams primed", async () => (await invoke<{ alerts: unknown[] }>("monitor:state")).data.alerts.length > 0);

  const apps = (await invoke<{ id: string; name: string }[]>("data", "apps", ["acme"])).data;
  const shop = apps.find((a) => a.name === "shop");
  const gw = apps.find((a) => a.name === "api-gateway");
  if (!shop || !gw) throw new Error("fixtures missing");
  const servers = (await invoke<{ servers: { id: string; name: string }[] }>("data", "servers", ["acme"])).data.servers;
  const db1 = servers.find((s) => s.name === "db-1");

  const screens = async (loc: "ar" | "en", theme: "dark" | "light") => {
    await invoke("prefs:set", { locale: loc, theme });
    const p = `${loc}-${theme}`;
    await go("#/t/acme", "main h1");
    await waitFor("overview data", () => ev<boolean>("document.querySelectorAll('main .grid button').length >= 3"));
    await shot(`${p}-overview`);
    await go("#/t/acme/apps", "main h1");
    await waitFor("apps data", () => ev<boolean>("document.querySelectorAll('main button').length >= 4"));
    await shot(`${p}-apps`);
    await go(`#/t/acme/apps/${gw.id}`, "[data-action]");
    await waitFor("deployments", () => ev<boolean>("document.querySelectorAll('main [aria-expanded]').length >= 1"));
    await clickText("main [aria-expanded]", "");
    await sleep(500);
    await shot(`${p}-app-detail`);
    await go(`#/t/acme/apps/${shop.id}`, "[data-action]");
    await waitFor("shop actions", () => ev<boolean>("!!document.querySelector('[data-action=redeploy]:not([disabled])')"));
    await ev("document.querySelector('[data-action=redeploy]').click()");
    await waitFor("dialog", () => ev<boolean>("!!document.querySelector('[role=dialog] input')"));
    await ev(`(() => { const i = document.querySelector('[role=dialog] input'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(i, 'shop'); i.dispatchEvent(new Event('input', {bubbles:true})) })()`);
    await shot(`${p}-app-confirm`);
    await ev("document.dispatchEvent(new KeyboardEvent('keydown', {key:'Escape'})); window.dispatchEvent(new KeyboardEvent('keydown', {key:'Escape'}))");
    await go("#/t/acme/alerts", "main h1");
    await waitFor("alerts data", () => ev<boolean>("document.querySelectorAll('main .space-y-2 > div').length >= 2"));
    await shot(`${p}-alerts`);
    await go("#/settings", "main h1");
    await waitFor("settings prefs", () => ev<boolean>("document.querySelectorAll('[role=switch]').length >= 8"));
    await shot(`${p}-settings`);
  };
  await screens("ar", "dark");
  await screens("en", "light");
  await screens("ar", "light");
  await screens("en", "dark");

  // ── extra pages (Arabic, dark)
  await invoke("prefs:set", { locale: "ar", theme: "dark" });
  if (db1) {
    await go(`#/t/acme/servers/${db1.id}`, "main h1");
    await waitFor("server detail", () => ev<boolean>("document.querySelectorAll('main svg polyline').length >= 1"));
    await shot("ar-dark-server");
  }
  await go("#/t/acme/domains", "main h1");
  await waitFor("domains", () => ev<boolean>("document.querySelectorAll('main .space-y-2 > div').length >= 2"));
  await shot("ar-dark-domains");
  await go("#/t/acme/backups", "main h1");
  await waitFor("backups", () => ev<boolean>("document.querySelectorAll('main .space-y-2 > div').length >= 2"));
  await shot("ar-dark-backups");

  // ── alert path: a new alert on the mock must raise a notification and turn the tray red
  const before = hooks().raised.length;
  const lvl0 = hooks().trayLevel();
  await fetch(`${API}/__mock/notify`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ team: "acme", kind: "app.down", title: "shop is down", body: "Health check failed on edge-1", subject_type: "app", subject_id: shop.id }),
  });
  await waitFor("native notification", () => hooks().raised.length > before, 15_000);
  await waitFor("tray red", () => hooks().trayLevel() === "red", 15_000);
  const n = hooks().raised[hooks().raised.length - 1];
  results.notification = { title: n?.title, hash: n?.hash, trayBefore: lvl0, trayAfter: hooks().trayLevel() };

  // ── a click on the notification / tray item routes to the app; so does a hosbah:// link
  hooks().openHash(n!.hash);
  await waitFor("alert deep link", () => ev<boolean>(`location.hash === ${JSON.stringify(n!.hash)} && !!document.querySelector('[data-action]')`));
  results.deepLinkFromNotification = n!.hash;
  hooks().handleLink("hosbah://t/acme/domains");
  await waitFor("hosbah:// link", () => ev<boolean>("location.hash === '#/t/acme/domains'"));
  results.deepLinkFromUrl = "#/t/acme/domains";

  // ── tray preview (the native menu cannot be captured; this draws the same model)
  for (const [loc, theme] of [["ar", "dark"], ["en", "light"]] as const) {
    await invoke("prefs:set", { locale: loc, theme });
    await go("#/tray-preview", "[role=menu]");
    await shot(`${loc}-${theme}-tray-menu`);
  }
  results.trayModel = hooks().trayModel();

  // ── the one allowed mutation: confirm a redeploy against the mock (never a real server)
  await invoke("prefs:set", { locale: "en", theme: "light" });
  await go(`#/t/acme/apps/${shop.id}`, "[data-action]");
  await ev("document.querySelector('[data-action=redeploy]').click()");
  await waitFor("dialog", () => ev<boolean>("!!document.querySelector('[role=dialog] input')"));
  const blocked = await ev<boolean>("[...document.querySelectorAll('[role=dialog] button')].some(b => b.textContent.includes('Redeploy') && b.disabled)");
  await ev(`(() => { const i = document.querySelector('[role=dialog] input'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(i, 'shop'); i.dispatchEvent(new Event('input', {bubbles:true})) })()`);
  await clickText("[role=dialog] button", "Redeploy");
  await waitFor("notice", () => ev<boolean>("!!document.querySelector('[role=status]')"));
  results.confirmBlockedUntilTyped = blocked;
  results.mockCalls = await (await fetch(`${API}/__mock/calls`)).json();

  writeFileSync(join(OUT, "desktop-smoke.json"), JSON.stringify(results, null, 2));
  log(`smoke: ${JSON.stringify(results)}`);
}

void app.whenReady().then(async () => {
  try {
    await main();
    log("done");
    app.exit(0);
  } catch (e) {
    process.stderr.write(`[shots] FAILED: ${e instanceof Error ? e.stack : String(e)}\n`);
    try {
      const img = await w().webContents.capturePage();
      writeFileSync(join(OUT, "desktop-failure.png"), img.toPNG());
    } catch {
      /* no window */
    }
    app.exit(1);
  }
});
