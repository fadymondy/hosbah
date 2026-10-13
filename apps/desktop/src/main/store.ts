// What the app remembers between launches. Credentials (JWT, rotating refresh token, device id)
// live only in `secret`, encrypted by the OS keychain through Electron's safeStorage (DPAPI on
// Windows, Keychain on macOS, libsecret/kwallet on Linux): an encrypted file in userData, never
// plain text. Without a usable keychain nothing secret is written and the person signs in each launch.
import { app, safeStorage } from "electron";
import { existsSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { DEFAULT_API_URL, type Locale, type Session, type TeamSummary } from "@hosbah/client";

export interface Stored {
  apiUrl: string;
  email: string;
  userId: string;
  /** Slug of the team the app is looking at. */
  activeTeam: string | null;
  /** Last known teams, so an offline launch still knows who you are. Not secret. */
  teams: TeamSummary[];
  /** safeStorage-encrypted JSON of the Session, base64. */
  secret?: string;
}

export interface Prefs {
  theme: "system" | "light" | "dark";
  locale: Locale;
  apiUrl: string;
  launchAtLogin: boolean;
}

export const defaultPrefs = (osLocale: string): Prefs => ({
  theme: "system",
  locale: /^en(?:[-_]|$)/i.test(osLocale) ? "en" : "ar",
  apiUrl: process.env.HOSBAH_API_URL || DEFAULT_API_URL,
  launchAtLogin: false,
});

const file = (name: string) => join(app.getPath("userData"), name);

/** Linux without a keyring falls back to a fixed key ("basic_text"), which is not a keychain. */
export function canEncrypt(): boolean {
  if (!safeStorage.isEncryptionAvailable()) return false;
  if (process.platform === "linux" && safeStorage.getSelectedStorageBackend?.() === "basic_text") return false;
  return true;
}

function readJson<T>(name: string): T | null {
  try {
    return existsSync(file(name)) ? (JSON.parse(readFileSync(file(name), "utf8")) as T) : null;
  } catch {
    return null;
  }
}

/** Write-then-rename, so a crash mid-write cannot leave a half-written (and so lost) rotated token. */
function writeJson(name: string, value: unknown): void {
  const target = file(name);
  mkdirSync(dirname(target), { recursive: true });
  const tmp = `${target}.tmp`;
  writeFileSync(tmp, JSON.stringify(value, null, 2), { mode: 0o600 });
  renameSync(tmp, target);
}

export function loadSession(): { stored: Stored; session: Session | null } | null {
  const stored = readJson<Stored>("session.json");
  if (!stored) return null;
  let session: Session | null = null;
  if (stored.secret && canEncrypt()) {
    try {
      session = JSON.parse(safeStorage.decryptString(Buffer.from(stored.secret, "base64"))) as Session;
    } catch {
      session = null;
    }
  }
  return { stored, session };
}

export function saveSession(meta: Omit<Stored, "secret">, session: Session | null): void {
  const out: Stored = { ...meta };
  if (session && canEncrypt()) out.secret = safeStorage.encryptString(JSON.stringify(session)).toString("base64");
  writeJson("session.json", out);
}

export function clearSession(): void {
  rmSync(file("session.json"), { force: true });
}

export function loadPrefs(): Prefs {
  return { ...defaultPrefs(app.getLocale()), ...(readJson<Partial<Prefs>>("prefs.json") ?? {}) };
}

export function savePrefs(p: Prefs): void {
  writeJson("prefs.json", p);
}
