// The only thing the renderer can reach: an allowlisted invoke + event subscription. No Node, no
// token, no arbitrary channel.
import { contextBridge, ipcRenderer } from "electron";
import { EVENT_CHANNELS, INVOKE_CHANNELS, type BridgeApi, type EventChannel, type InvokeChannel } from "../core/ipc";

const bridge: BridgeApi = {
  invoke(channel: InvokeChannel, ...args: unknown[]) {
    if (!(INVOKE_CHANNELS as readonly string[]).includes(channel)) return Promise.reject(new Error(`blocked channel: ${channel}`));
    return ipcRenderer.invoke(channel, ...args);
  },
  on(channel: EventChannel, cb: (payload: unknown) => void) {
    if (!(EVENT_CHANNELS as readonly string[]).includes(channel)) throw new Error(`blocked channel: ${channel}`);
    const h = (_e: unknown, payload: unknown) => cb(payload);
    ipcRenderer.on(channel, h);
    return () => ipcRenderer.removeListener(channel, h);
  },
};

contextBridge.exposeInMainWorld("hosbah", bridge);
