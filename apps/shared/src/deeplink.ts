import type { PushData } from "./types";

export type LinkScreen = "app" | "server" | "domains" | "backups" | "alerts";

export interface DeepLink {
  team: string;
  screen: LinkScreen;
  id?: string;
}

export const APP_SCHEME = "hosbah";

/**
 * hosbah://t/<team>/apps/<id> | servers/<id> | domains | backups | alerts, and the same path on an
 * https URL (https://<host>/t/<team>/...). Anything else is null.
 */
export function parseDeepLink(url: string | null | undefined): DeepLink | null {
  if (!url) return null;
  const m = /^(?:hosbah:\/\/|https?:\/\/[^/?#]+\/)(t\/[^?#]*)/i.exec(url.trim());
  if (!m || !m[1]) return null;
  const seg = m[1].split("/");
  while (seg.length && seg[seg.length - 1] === "") seg.pop();
  if (seg[0]?.toLowerCase() !== "t" || !seg[1]) return null;
  let team: string;
  let id: string | undefined;
  try {
    team = decodeURIComponent(seg[1]);
    id = seg[3] !== undefined ? decodeURIComponent(seg[3]) : undefined;
  } catch {
    return null;
  }
  const kind = seg[2];
  if ((kind === "apps" || kind === "servers") && id && seg.length === 4) {
    return { team, screen: kind === "apps" ? "app" : "server", id };
  }
  if ((kind === "domains" || kind === "backups" || kind === "alerts") && seg.length === 3) {
    return { team, screen: kind };
  }
  return null;
}

export function toDeepLink(l: DeepLink): string {
  const t = encodeURIComponent(l.team);
  switch (l.screen) {
    case "app":
      return `${APP_SCHEME}://t/${t}/apps/${encodeURIComponent(l.id ?? "")}`;
    case "server":
      return `${APP_SCHEME}://t/${t}/servers/${encodeURIComponent(l.id ?? "")}`;
    default:
      return `${APP_SCHEME}://t/${t}/${l.screen}`;
  }
}

/** Where a tapped push goes: its `url` when valid, else derived from team / subject_type / subject_id. */
export function pushDataToLink(data: PushData | Record<string, unknown> | null | undefined): DeepLink | null {
  if (!data) return null;
  const d = data as PushData;
  const fromUrl = parseDeepLink(typeof d.url === "string" ? d.url : undefined);
  if (fromUrl) return fromUrl;
  if (!d.team) return null;
  const id = d.subject_id;
  switch (d.subject_type) {
    case "app":
      return id ? { team: d.team, screen: "app", id } : { team: d.team, screen: "alerts" };
    case "server":
      return id ? { team: d.team, screen: "server", id } : { team: d.team, screen: "alerts" };
    case "certificate":
    case "domain":
      return { team: d.team, screen: "domains" };
    case "data_service":
    case "backup":
      return { team: d.team, screen: "backups" };
    default:
      return { team: d.team, screen: "alerts" };
  }
}
