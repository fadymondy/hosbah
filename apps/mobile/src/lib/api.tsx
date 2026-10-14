import { createAsyncStoragePersister } from "@tanstack/query-async-storage-persister";
import { QueryClient } from "@tanstack/react-query";
import { PersistQueryClientProvider } from "@tanstack/react-query-persist-client";
import AsyncStorage from "@react-native-async-storage/async-storage";
import * as Device from "expo-device";
import { ApiError, HosbahClient, normalizeApiUrl, twoFactorChallenge, type AuthProvider, type DeleteAccountProof, type LoginResult, type RegisterDeviceInput } from "@hosbah/client";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Platform } from "react-native";

import { APP_VERSION, DEFAULT_SERVER, IS_MOCK } from "@/lib/config";
import { usePrefs } from "@/lib/prefs";
import { loadSession, saveSession } from "@/lib/session";
import { socialSignIn } from "@/lib/social";
import { getStored, KEYS, removeStored, setStored } from "@/lib/storage";

export type AuthStatus = "loading" | "out" | "in";

export type SignInResult = { twoFactor: string } | { twoFactor?: undefined };

/** A new account has to confirm its address first ({verify}); an invitation signs straight in. */
export type SignUpResult = { verify: string; twoFactor?: undefined } | (SignInResult & { verify?: undefined });

interface Api {
  client: HosbahClient;
  status: AuthStatus;
  serverUrl: string;
  /** Switching servers signs this device out of the old one first. */
  setServerUrl: (url: string) => Promise<void>;
  signIn: (email: string, password: string) => Promise<SignInResult>;
  /** Google / Apple / GitHub (src/lib/social.ts); throws SocialCancelled when the person backs out. */
  signInWith: (provider: AuthProvider) => Promise<SignInResult>;
  /** POST /api/auth/register. */
  signUp: (email: string, password: string) => Promise<SignUpResult>;
  /** The 6-digit code from the sign-up email; activates the account and signs in. */
  verifyEmail: (email: string, code: string) => Promise<SignInResult>;
  completeTwoFactor: (challenge: string, answer: { code?: string; recovery_code?: string }) => Promise<void>;
  signOut: () => Promise<void>;
  /** DELETE /api/me with one proof; on success the device is signed out (the account is gone). */
  deleteAccount: (proof: DeleteAccountProof) => Promise<void>;
}

const Ctx = createContext<Api | null>(null);

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 20_000,
        gcTime: 24 * 3_600_000,
        retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2,
        refetchOnWindowFocus: false,
      },
    },
  });
}

const persister = createAsyncStoragePersister({ storage: AsyncStorage, key: KEYS.queryCache, throttleTime: 1500 });

export function deviceRegistration(locale: "ar" | "en"): RegisterDeviceInput {
  return {
    name: Device.deviceName || Device.modelName || (Platform.OS === "ios" ? "iPhone" : "Android phone"),
    platform: Platform.OS === "ios" ? "ios" : "android",
    app: "mobile",
    locale,
    app_version: APP_VERSION,
  };
}

async function makeClient(serverUrl: string, onAuthRequired: () => void): Promise<HosbahClient> {
  const session = await loadSession();
  let fetchImpl: typeof fetch | undefined;
  if (IS_MOCK) {
    const { createMockFetch } = await import("@/lib/mock");
    fetchImpl = createMockFetch({ latencyMs: 220 }).fetch;
  }
  return new HosbahClient({
    baseUrl: serverUrl,
    session,
    onSession: saveSession,
    onAuthRequired,
    ...(fetchImpl ? { fetch: fetchImpl } : {}),
  });
}

export function ApiProvider({ children }: { children: ReactNode }) {
  const { locale } = usePrefs();
  const localeRef = useRef(locale);
  localeRef.current = locale;

  const queryClient = useMemo(createQueryClient, []);
  const [client, setClientState] = useState<HosbahClient | null>(null);
  // Read by the callbacks below so a server switch is visible to them in the same tick (state is not).
  const clientRef = useRef<HosbahClient | null>(null);
  const setClient = useCallback((c: HosbahClient) => {
    clientRef.current = c;
    setClientState(c);
  }, []);
  const [status, setStatus] = useState<AuthStatus>("loading");
  const [serverUrl, setServerUrlState] = useState(DEFAULT_SERVER);

  const dropCache = useCallback(async () => {
    queryClient.clear();
    await removeStored(KEYS.queryCache);
  }, [queryClient]);

  const onAuthRequired = useCallback(() => {
    void dropCache();
    setStatus("out");
  }, [dropCache]);

  useEffect(() => {
    void (async () => {
      const stored = await getStored(KEYS.server);
      const url = stored ? normalizeApiUrl(stored) : DEFAULT_SERVER;
      setServerUrlState(url);
      const c = await makeClient(url, onAuthRequired);
      setClient(c);
      if (IS_MOCK && process.env.EXPO_PUBLIC_MOCK_SIGNED_IN === "1" && !c.signedIn) {
        await c.login("demo@example.com", "demo");
        await c.registerDevice(deviceRegistration(localeRef.current));
      }
      setStatus(c.signedIn ? "in" : "out");
    })();
  }, [onAuthRequired, setClient]);

  const finishSignIn = useCallback(
    async (c: HosbahClient) => {
      try {
        await c.registerDevice(deviceRegistration(localeRef.current));
      } catch (e) {
        await c.signOut();
        throw e;
      }
      setStatus("in");
    },
    [],
  );

  // Any first factor (password, a provider): a 2FA challenge comes back to the screen, anything else
  // registers the device.
  const firstFactor = useCallback(
    async (fn: (c: HosbahClient) => Promise<LoginResult>): Promise<SignInResult> => {
      const client = clientRef.current;
      if (!client) throw new Error("not ready");
      try {
        await fn(client);
      } catch (e) {
        const challenge = twoFactorChallenge(e);
        if (challenge) return { twoFactor: challenge };
        throw e;
      }
      await finishSignIn(client);
      return {};
    },
    [finishSignIn],
  );

  const signIn = useCallback<Api["signIn"]>(
    (email, password) => firstFactor((c) => c.login(email.trim(), password, localeRef.current)),
    [firstFactor],
  );

  const signInWith = useCallback<Api["signInWith"]>(
    (provider) => firstFactor((c) => socialSignIn(c, provider, localeRef.current)),
    [firstFactor],
  );

  const signUp = useCallback<Api["signUp"]>(
    async (email, password) => {
      const client = clientRef.current;
      if (!client) throw new Error("not ready");
      const r = await client.register({ email: email.trim(), password, locale: localeRef.current });
      if (r.verify !== undefined) return { verify: r.verify };
      const session = r.session;
      return firstFactor(async () => session);
    },
    [firstFactor],
  );

  const verifyEmail = useCallback<Api["verifyEmail"]>(
    (email, code) => firstFactor((c) => c.verifyEmail(email.trim(), code.trim())),
    [firstFactor],
  );

  const completeTwoFactor = useCallback<Api["completeTwoFactor"]>(
    async (challenge, answer) => {
      const client = clientRef.current;
      if (!client) throw new Error("not ready");
      await client.completeTwoFactor(challenge, answer);
      await finishSignIn(client);
    },
    [finishSignIn],
  );

  const signOut = useCallback(async () => {
    if (client) await client.signOut();
    await dropCache();
    await removeStored(KEYS.pushToken);
    setStatus("out");
  }, [client, dropCache]);

  const deleteAccount = useCallback<Api["deleteAccount"]>(
    async (proof) => {
      if (!client) throw new Error("not ready");
      await client.deleteAccount(proof);
      await dropCache();
      await removeStored(KEYS.pushToken);
      setStatus("out");
    },
    [client, dropCache],
  );

  const setServerUrl = useCallback(
    async (input: string) => {
      const url = normalizeApiUrl(input);
      if (url === serverUrl) return;
      if (client?.signedIn) await client.signOut();
      await dropCache();
      await setStored(KEYS.server, url);
      setServerUrlState(url);
      setClient(await makeClient(url, onAuthRequired));
      setStatus("out");
    },
    [client, dropCache, onAuthRequired, serverUrl, setClient],
  );

  const value = useMemo<Api | null>(
    () => (client ? { client, status, serverUrl, setServerUrl, signIn, signInWith, signUp, verifyEmail, completeTwoFactor, signOut, deleteAccount } : null),
    [client, status, serverUrl, setServerUrl, signIn, signInWith, signUp, verifyEmail, completeTwoFactor, signOut, deleteAccount],
  );

  return (
    <PersistQueryClientProvider
      client={queryClient}
      persistOptions={{
        persister,
        maxAge: 24 * 3_600_000,
        buster: `${APP_VERSION}:${serverUrl}`,
        dehydrateOptions: { shouldDehydrateQuery: (q) => q.state.status === "success" },
      }}
    >
      {value ? <Ctx.Provider value={value}>{children}</Ctx.Provider> : null}
    </PersistQueryClientProvider>
  );
}

export function useApi(): Api {
  const v = useContext(Ctx);
  if (!v) throw new Error("useApi outside ApiProvider");
  return v;
}
