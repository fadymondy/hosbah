const mockStore = new Map<string, string>();
jest.mock("expo-secure-store", () => ({
  AFTER_FIRST_UNLOCK: 1,
  getItemAsync: jest.fn(async (k: string) => mockStore.get(k) ?? null),
  setItemAsync: jest.fn(async (k: string, v: string) => void mockStore.set(k, v)),
  deleteItemAsync: jest.fn(async (k: string) => void mockStore.delete(k)),
}));

import { loadSession, saveSession } from "@/lib/session";

beforeEach(() => mockStore.clear());

describe("session store", () => {
  it("round-trips token, rotated refresh token and device id", async () => {
    expect(await loadSession()).toBeNull();
    await saveSession({ token: "jwt1", refreshToken: "r1", deviceId: "d1" });
    expect(await loadSession()).toEqual({ token: "jwt1", refreshToken: "r1", deviceId: "d1" });
    await saveSession({ token: "jwt2", refreshToken: "r2", deviceId: "d1" });
    expect((await loadSession())?.refreshToken).toBe("r2");
  });
  it("clears everything on sign-out", async () => {
    await saveSession({ token: "jwt1", refreshToken: "r1", deviceId: "d1" });
    await saveSession(null);
    expect(mockStore.size).toBe(0);
    expect(await loadSession()).toBeNull();
  });
});
