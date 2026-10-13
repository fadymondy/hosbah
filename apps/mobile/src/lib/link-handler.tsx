import type { Href } from "expo-router";
import { useRouter } from "expo-router";
import { useEffect } from "react";

import { onLink, routeFor, takePendingLink } from "@/lib/links";
import { selectTeam } from "@/lib/team";

/**
 * Opens whatever destination is waiting in the link queue (push tap, hosbah:// URL), switching team
 * first. Mounted only while signed in, so a link that arrives on a cold start or before sign-in waits
 * here and is honoured right after.
 */
export function LinkHandler() {
  const router = useRouter();
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const run = () => {
      const d = takePendingLink();
      if (!d) return;
      if (d.team) selectTeam(d.team);
      // Let the navigator settle on the root first: redirectSystemPath sends the router to "/" right after.
      clearTimeout(timer);
      timer = setTimeout(() => {
        if (router.canDismiss()) router.dismissAll();
        router.navigate(routeFor(d) as Href);
      }, 80);
    };
    run();
    const off = onLink(run);
    return () => {
      off();
      clearTimeout(timer);
    };
  }, [router]);
  return null;
}
