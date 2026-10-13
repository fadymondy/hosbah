import type { AppStatus } from "@hosbah/client";
import { Play, RotateCcw, Square, UploadCloud } from "lucide-react-native";
import { useState } from "react";
import { View } from "react-native";

import { authenticateForAction } from "@/lib/confirm";
import { errorKey } from "@/lib/errors";
import { useColors, useT } from "@/lib/prefs";
import { useAppAction, useCurrentTeam, type AppAction } from "@/lib/queries";
import { Button } from "@/ui/button";
import { ConfirmDialog } from "@/ui/confirm-dialog";
import { useToast } from "@/ui/toast";
import { space } from "@/ui/theme";

/** Which actions make sense for an app in this status. */
export function actionsFor(status: AppStatus): AppAction[] {
  if (status === "stopped") return ["start", "redeploy"];
  if (status === "deploying") return [];
  if (status === "new") return ["redeploy"];
  return ["redeploy", "restart", "stop"];
}

/**
 * Redeploy / Restart / Start / Stop. Each asks on screen first, then for the device lock (biometrics,
 * falling back to the passcode; skipped when the phone has none), then calls the API.
 * Renders nothing for viewers: they get read-only screens.
 */
export function AppActions({ appId, appName, status }: { appId: string; appName: string; status: AppStatus }) {
  const t = useT();
  const c = useColors();
  const toast = useToast();
  const { canOperate } = useCurrentTeam();
  const mutation = useAppAction(appId);
  const [pending, setPending] = useState<AppAction | null>(null);
  const [busy, setBusy] = useState(false);

  if (!canOperate) return null;
  const actions = actionsFor(status);
  if (actions.length === 0) return null;

  const label = (a: AppAction) => t(`action.${a}`);
  const icon = (a: AppAction) => {
    const p = { size: 18, color: a === "stop" ? c.dangerText : c.fg };
    return a === "redeploy" ? <UploadCloud {...p} /> : a === "restart" ? <RotateCcw {...p} /> : a === "start" ? <Play {...p} /> : <Square {...p} />;
  };

  const confirm = async () => {
    if (!pending) return;
    const action = pending;
    setBusy(true);
    try {
      const ok = await authenticateForAction(t("biometric.reason", { action: label(action) }), t("confirm.cancel"));
      if (!ok) return;
      await mutation.mutateAsync(action);
      toast(t("action.queued", { action: label(action) }));
    } catch (e) {
      toast(t(errorKey(e)), "error");
    } finally {
      setBusy(false);
      setPending(null);
    }
  };

  return (
    <>
      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: space.sm }} testID="app-actions">
        {actions.map((a) => (
          <Button
            key={a}
            testID={`action-${a}`}
            kind={a === "stop" ? "danger" : "secondary"}
            label={label(a)}
            icon={icon(a)}
            onPress={() => setPending(a)}
            style={{ flexGrow: 1 }}
          />
        ))}
      </View>
      <ConfirmDialog
        visible={pending !== null}
        title={pending ? t(`confirm.${pending}`, { app: appName }) : ""}
        confirmLabel={pending ? label(pending) : t("confirm.yes")}
        destructive={pending === "stop"}
        busy={busy}
        onConfirm={() => void confirm()}
        onCancel={() => !busy && setPending(null)}
      />
    </>
  );
}
