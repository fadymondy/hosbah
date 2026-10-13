// Hosbah desktop: window, tray, single instance, hosbah:// links, background alert monitoring.
import { app, BrowserWindow, Menu, nativeTheme, shell } from "electron";
import { join } from "node:path";
import { brandName } from "@hosbah/client/brand";
import { findDeepLinkArg, hashForUrl } from "../core/route";
import * as api from "./api";
import * as monitor from "./monitor";
import * as session from "./session";
import * as store from "./store";
import * as tray from "./tray";

const E2E = process.env.HOSBAH_E2E === "1";

// Test runs get their own data folder so they never touch a real sign-in.
if (process.env.HOSBAH_USER_DATA) app.setPath("userData", process.env.HOSBAH_USER_DATA);
app.setAppUserModelId("com.fadymondy.hosbah.desktop");

let win: BrowserWindow | null = null;
let quitting = false;
let pendingHash: string | null = null;
let rendererReady = false;

const send = (channel: string, payload: unknown) => {
  if (win && !win.isDestroyed() && rendererReady) win.webContents.send(channel, payload);
};

function showWindow(): void {
  if (!win || win.isDestroyed()) createWindow();
  if (!win) return;
  if (win.isMinimized()) win.restore();
  win.show();
  win.focus();
}

/** Brings the window up on a route (#/t/<team>/apps/<id>). */
export function openHash(hash: string): void {
  showWindow();
  if (rendererReady) send("navigate", hash);
  else pendingHash = hash;
}

function handleLink(url: string): void {
  const hash = hashForUrl(url);
  if (hash) openHash(hash);
  else showWindow();
}

const actions: tray.TrayActions = {
  open: () => showWindow(),
  openHash,
  quit: () => {
    quitting = true;
    app.quit();
  },
};

function createWindow(): void {
  const prefs = store.loadPrefs();
  rendererReady = false;
  win = new BrowserWindow({
    width: 1180,
    height: 780,
    minWidth: 860,
    minHeight: 560,
    show: !process.argv.includes("--hidden"),
    title: brandName[prefs.locale],
    backgroundColor: nativeTheme.shouldUseDarkColors ? "#0b1429" : "#f0ebe1",
    autoHideMenuBar: true,
    icon: join(__dirname, "..", "build", "icon.png"),
    webPreferences: {
      preload: join(__dirname, "preload.cjs"),
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
      spellcheck: false,
    },
  });
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (/^https:\/\//i.test(url)) void shell.openExternal(url);
    return { action: "deny" };
  });
  win.webContents.on("will-navigate", (e, url) => {
    if (!url.startsWith("file:") && !url.startsWith(process.env.HOSBAH_DEV_URL ?? "\0")) e.preventDefault();
  });
  win.webContents.on("did-finish-load", () => {
    rendererReady = true;
    if (pendingHash) {
      send("navigate", pendingHash);
      pendingHash = null;
    }
  });
  // Closing the window keeps the app alive in the tray; Quit in the tray menu really quits.
  win.on("close", (e) => {
    if (!quitting && !E2E) {
      e.preventDefault();
      win?.hide();
    }
  });
  win.on("closed", () => {
    win = null;
    rendererReady = false;
  });
  const dev = process.env.HOSBAH_DEV_URL;
  if (dev) void win.loadURL(dev);
  else void win.loadFile(join(__dirname, "renderer", "index.html"));
}

function broadcastTray(): void {
  tray.update(monitor.state().tray, actions);
}

async function boot(): Promise<void> {
  Menu.setApplicationMenu(process.platform === "darwin" ? Menu.buildFromTemplate([{ role: "appMenu" }, { role: "editMenu" }, { role: "windowMenu" }]) : null);
  api.applyPrefs(store.loadPrefs());
  api.register((ch, payload) => {
    send(ch, payload);
    if (ch === "prefs" && win) win.setTitle(brandName[(payload as store.Prefs).locale]);
  });
  monitor.setOpenHandler(openHash);
  monitor.events.on("change", broadcastTray);
  session.events.on("expired", () => monitor.stop());
  session.events.on("signed-out", () => monitor.stop());

  broadcastTray();
  createWindow();
  await session.restore().catch(() => undefined);
  if (session.api()) {
    void monitor.loadPrefs();
    monitor.sync();
  }
  broadcastTray();
}

// ── single instance + hosbah:// ───────────────────────────────────────────────────────────────
const gotLock = E2E ? true : app.requestSingleInstanceLock();
if (!gotLock) {
  app.quit();
} else {
  if (!E2E) {
    if (process.defaultApp && process.argv[1]) app.setAsDefaultProtocolClient("hosbah", process.execPath, [process.argv[1]]);
    else app.setAsDefaultProtocolClient("hosbah");
  }
  app.on("second-instance", (_e, argv) => {
    const link = findDeepLinkArg(argv);
    if (link) handleLink(link);
    else showWindow();
  });
  app.on("open-url", (e, url) => {
    e.preventDefault();
    if (app.isReady()) handleLink(url);
    else pendingHash = hashForUrl(url);
  });
  app.on("before-quit", () => {
    quitting = true;
  });
  app.on("activate", () => showWindow());
  app.on("window-all-closed", () => {
    /* stay in the tray */
  });
  void app.whenReady().then(async () => {
    const first = findDeepLinkArg(process.argv);
    if (first) pendingHash = hashForUrl(first);
    await boot();
  });
}

// Test hooks (screenshots / e2e): the window and what the monitor raised.
if (E2E) {
  (globalThis as Record<string, unknown>).__hosbahTest = {
    win: () => win,
    raised: monitor.raised,
    trayLevel: () => tray.currentLevel,
    trayModel: () => monitor.state().tray,
    openHash,
    handleLink,
  };
}
