// Typed access to the preload bridge. Every call resolves to data or throws a BridgeError that
// keeps the API's status and code, so views can tell "forbidden" from "offline".
import type { BridgeApi, EventChannel, InvokeChannel, Reply } from "../../core/ipc";

declare global {
  interface Window {
    hosbah: BridgeApi;
  }
}

export class BridgeError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly code?: string,
  ) {
    super(message);
  }
  get offline(): boolean {
    return this.status === 0 && this.code === "network";
  }
  get forbidden(): boolean {
    return this.status === 403;
  }
}

export async function call<T>(channel: InvokeChannel, ...args: unknown[]): Promise<T> {
  const r = (await window.hosbah.invoke(channel, ...args)) as Reply<T>;
  if (r.ok) return r.data;
  throw new BridgeError(r.status, r.message, r.code);
}

/** A read on the signed-in API client (allowlisted in the main process). */
export const data = <T>(fn: string, ...args: unknown[]): Promise<T> => call<T>("data", fn, args);

export const on = (channel: EventChannel, cb: (payload: never) => void): (() => void) => window.hosbah.on(channel, cb as (p: unknown) => void);
