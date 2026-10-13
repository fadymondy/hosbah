// The tray / menu-bar item: a coloured disc (green / amber / red), a tooltip and a small menu.
import { Menu, Tray, nativeImage, type NativeImage } from "electron";
import type { Overall } from "@hosbah/client";
import { statusIconPng } from "../core/png";
import type { TrayModel } from "../core/trayModel";

let tray: Tray | null = null;
const cache = new Map<Overall, NativeImage>();
export let currentLevel: Overall | null = null;

function icon(level: Overall): NativeImage {
  let img = cache.get(level);
  if (!img) {
    // 16 px at 1x, 32 px for HiDPI: the OS picks the right one.
    img = nativeImage.createEmpty();
    img.addRepresentation({ scaleFactor: 1, width: 16, height: 16, buffer: statusIconPng(level, 16) });
    img.addRepresentation({ scaleFactor: 2, width: 32, height: 32, buffer: statusIconPng(level, 32) });
    cache.set(level, img);
  }
  return img;
}

export interface TrayActions {
  open(): void;
  openHash(hash: string): void;
  quit(): void;
}

export function menuFor(m: TrayModel, a: TrayActions): Menu {
  const items: Electron.MenuItemConstructorOptions[] = [
    { label: m.statusLine, enabled: false },
    { label: m.unreadLine, enabled: false },
    { type: "separator" },
    { label: m.openLabel, click: () => a.open() },
    { type: "separator" },
    { label: m.recentTitle, enabled: false },
  ];
  if (m.recent.length === 0) items.push({ label: m.emptyLine, enabled: false });
  for (const r of m.recent) items.push({ label: `${r.unread ? "● " : ""}${r.title}`, click: () => a.openHash(r.hash) });
  items.push({ type: "separator" }, { label: m.quitLabel, click: () => a.quit() });
  return Menu.buildFromTemplate(items);
}

export function update(m: TrayModel, a: TrayActions): void {
  currentLevel = m.level;
  if (!tray) {
    tray = new Tray(icon(m.level));
    tray.on("click", () => a.open());
  }
  tray.setImage(icon(m.level));
  tray.setToolTip(m.tooltip);
  tray.setContextMenu(menuFor(m, a));
  if (process.platform === "darwin") tray.setTitle(m.unread > 0 ? ` ${m.unread}` : "");
}

export function destroy(): void {
  tray?.destroy();
  tray = null;
}
