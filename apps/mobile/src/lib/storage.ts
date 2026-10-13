import AsyncStorage from "@react-native-async-storage/async-storage";

// Small, non-secret preferences (language, theme, server URL, selected team). Secrets go through
// session.ts (SecureStore), never here.

export async function getStored(key: string): Promise<string | null> {
  try {
    return await AsyncStorage.getItem(key);
  } catch {
    return null;
  }
}

export async function setStored(key: string, value: string): Promise<void> {
  try {
    await AsyncStorage.setItem(key, value);
  } catch {
    // Storage full or unavailable: the preference just does not persist.
  }
}

export async function removeStored(key: string): Promise<void> {
  try {
    await AsyncStorage.removeItem(key);
  } catch {
    // ignore
  }
}

export const KEYS = {
  locale: "hosbah.locale",
  theme: "hosbah.theme",
  server: "hosbah.server",
  team: "hosbah.team",
  rtlReload: "hosbah.rtlReloadFor",
  pushToken: "hosbah.push.token",
  queryCache: "hosbah.query-cache",
} as const;
