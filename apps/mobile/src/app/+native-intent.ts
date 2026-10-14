import { dispatchLink, parseAnyLink } from "@/lib/links";
import { isOAuthReturn } from "@/lib/oauth";

// Every URL the OS hands the app (hosbah://t/<team>/apps/<id>, https links, hosbah://alerts) passes
// through here before the router sees it. The routes do not match the URL shape, so instead of letting
// expo-router show "unmatched route" we queue the destination, which the signed-in navigator opens
// (switching team first) once it is mounted, and land on the root meanwhile.
export function redirectSystemPath({ path }: { path: string; initial: boolean }): string {
  // hosbah://oauth?code=… is the browser sign-in coming back. The auth session reads it (lib/oauth.ts);
  // on Android the same URL also reaches the router, which must not show "unmatched route" for it.
  if (isOAuthReturn(path)) return "/";
  // Depending on the platform the path is the full URL or just "/t/<team>/...".
  const dest = parseAnyLink(path) ?? parseAnyLink("hosbah://" + path.replace(/^\/+/, ""));
  if (!dest) return path;
  dispatchLink(dest);
  return "/";
}
