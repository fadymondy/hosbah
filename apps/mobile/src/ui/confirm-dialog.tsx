import { Modal, Pressable, View } from "react-native";

import { useT } from "@/lib/prefs";
import { Button } from "@/ui/button";
import { AppText } from "@/ui/text";
import { radius, space, useStyles } from "@/ui/theme";

interface Props {
  visible: boolean;
  title: string;
  message?: string;
  confirmLabel?: string;
  destructive?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ConfirmDialog({ visible, title, message, confirmLabel, destructive, busy, onConfirm, onCancel }: Props) {
  const t = useT();
  const s = useStyles((c) => ({
    scrim: { flex: 1, backgroundColor: "rgba(0,0,0,0.5)", justifyContent: "center", padding: space.xl },
    box: { backgroundColor: c.card, borderRadius: radius.lg, padding: space.xl, gap: space.md, borderWidth: 1, borderColor: c.line },
    row: { flexDirection: "row", gap: space.sm, marginTop: space.sm },
  }));
  return (
    <Modal visible={visible} transparent animationType="fade" onRequestClose={onCancel} statusBarTranslucent>
      <Pressable style={s.scrim} onPress={onCancel} accessibilityLabel={t("confirm.cancel")}>
        <Pressable style={s.box} onPress={() => undefined} testID="confirm-dialog">
          <AppText variant="heading">{title}</AppText>
          {message ? <AppText>{message}</AppText> : null}
          <View style={s.row}>
            <Button style={{ flex: 1 }} kind="secondary" label={t("confirm.cancel")} onPress={onCancel} disabled={busy} testID="confirm-cancel" />
            <Button
              style={{ flex: 1 }}
              kind={destructive ? "danger" : "primary"}
              label={confirmLabel ?? t("confirm.yes")}
              onPress={onConfirm}
              loading={!!busy}
              testID="confirm-yes"
            />
          </View>
        </Pressable>
      </Pressable>
    </Modal>
  );
}
