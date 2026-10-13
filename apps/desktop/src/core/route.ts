// Notification -> where it opens, and deep link -> the renderer's hash route. Pure.
import { parseDeepLink, pushDataToLink, type AppNotification, type DeepLink } from "@hosbah/client";

export interface AppRef {
  id: string;
  name: string;
}

/**
 * The deep link a notification opens: apps/<id>, servers/<id>, domains, backups, else alerts.
 * Deployment notifications carry the deployment id (not the app's), so the app is found by the
 * name in the title ("Deploy #4 of shop failed") when the caller knows the team's apps.
 */
export function routeForNotification(team: string, n: Pick<AppNotification, "kind" | "title" | "subject_type" | "subject_id">, apps: AppRef[] = []): DeepLink {
  const kind = n.kind;
  if (n.subject_type === "deployment" || kind.startsWith("deploy.")) {
    const hit = apps.find((a) => new RegExp(`\\b(?:of|for)\\s+${escapeRe(a.name)}(?:\\s|$|[.:,])`, "i").test(n.title));
    return hit ? { team, screen: "app", id: hit.id } : { team, screen: "alerts" };
  }
  if (kind === "cert.expiring") return { team, screen: "domains" };
  if (kind === "backup.failed") return { team, screen: "backups" };
  const viaSubject = pushDataToLink({ team, kind, subject_type: n.subject_type ?? undefined, subject_id: n.subject_id ?? undefined });
  if (viaSubject) return viaSubject;
  return { team, screen: "alerts" };
}

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/** The renderer's hash route for a link: #/t/<team>/apps/<id>. */
export function linkToHash(l: DeepLink): string {
  const t = encodeURIComponent(l.team);
  switch (l.screen) {
    case "app":
      return `#/t/${t}/apps/${encodeURIComponent(l.id ?? "")}`;
    case "server":
      return `#/t/${t}/servers/${encodeURIComponent(l.id ?? "")}`;
    default:
      return `#/t/${t}/${l.screen}`;
  }
}

/** First hosbah:// URL in a process argv (Windows/Linux pass the link as an argument). */
export function findDeepLinkArg(argv: readonly string[]): string | null {
  return argv.find((a) => /^hosbah:\/\//i.test(a) && parseDeepLink(a) !== null) ?? null;
}

/** hosbah://t/<team>/... -> hash route, or null for a link we do not understand. */
export function hashForUrl(url: string): string | null {
  const l = parseDeepLink(url);
  return l ? linkToHash(l) : null;
}
