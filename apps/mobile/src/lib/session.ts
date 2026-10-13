import * as SecureStore from "expo-secure-store";
import type { Session } from "@hosbah/client";

// The device session (JWT, rotating refresh token, device id) lives in the Keychain / Keystore.
// Three small items rather than one JSON blob: iOS Keychain values are happiest well under 2 KB, and a
// JWT plus a token already come close.

const K_TOKEN = "hosbah.session.token";
const K_REFRESH = "hosbah.session.refresh";
const K_DEVICE = "hosbah.session.device";

const OPTS: SecureStore.SecureStoreOptions = { keychainAccessible: SecureStore.AFTER_FIRST_UNLOCK };

async function get(key: string): Promise<string | null> {
  try {
    return await SecureStore.getItemAsync(key, OPTS);
  } catch {
    return null;
  }
}

async function put(key: string, value: string | null): Promise<void> {
  if (value) await SecureStore.setItemAsync(key, value, OPTS);
  else await SecureStore.deleteItemAsync(key, OPTS).catch(() => undefined);
}

export async function loadSession(): Promise<Session | null> {
  const [token, refreshToken, deviceId] = await Promise.all([get(K_TOKEN), get(K_REFRESH), get(K_DEVICE)]);
  if (!token && !refreshToken) return null;
  return { token: token ?? "", refreshToken, deviceId };
}

/**
 * Persists every session change HosbahClient reports (it rotates the refresh token on each refresh, so
 * this must finish before the client carries on). null = signed out: everything goes.
 */
export async function saveSession(s: Session | null): Promise<void> {
  await Promise.all([put(K_TOKEN, s?.token || null), put(K_REFRESH, s?.refreshToken ?? null), put(K_DEVICE, s?.deviceId ?? null)]);
}
