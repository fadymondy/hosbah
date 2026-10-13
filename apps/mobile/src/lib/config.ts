import * as Application from "expo-application";
import { DEFAULT_API_URL } from "@hosbah/client";

/** Dev-only fixture mode (EXPO_PUBLIC_MOCK=1): the app talks to src/lib/mock.ts, never to a server. */
export const IS_MOCK = process.env.EXPO_PUBLIC_MOCK === "1";

/** Where the sign-in screen starts; the user can change it there and in Settings. */
export const DEFAULT_SERVER: string = process.env.EXPO_PUBLIC_API_URL || DEFAULT_API_URL;

export const APP_VERSION: string = Application.nativeApplicationVersion ?? "1.0.0";
