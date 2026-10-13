import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useColors } from "@/lib/prefs";
import { AppText } from "@/ui/text";
import { radius, space } from "@/ui/theme";

type Kind = "info" | "error";
interface Toast {
  id: number;
  message: string;
  kind: Kind;
}

const Ctx = createContext<(message: string, kind?: Kind) => void>(() => undefined);

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<Toast | null>(null);
  const seq = useRef(0);
  const show = useCallback((message: string, kind: Kind = "info") => setToast({ id: ++seq.current, message, kind }), []);
  useEffect(() => {
    if (!toast) return;
    const h = setTimeout(() => setToast(null), 3500);
    return () => clearTimeout(h);
  }, [toast]);
  const value = useMemo(() => show, [show]);
  return (
    <Ctx.Provider value={value}>
      {children}
      {toast ? <ToastView toast={toast} /> : null}
    </Ctx.Provider>
  );
}

function ToastView({ toast }: { toast: Toast }) {
  const c = useColors();
  const insets = useSafeAreaInsets();
  return (
    <View pointerEvents="none" style={{ position: "absolute", left: space.lg, right: space.lg, bottom: insets.bottom + 80, alignItems: "center" }}>
      <View
        testID="toast"
        accessibilityLiveRegion="polite"
        style={{
          backgroundColor: toast.kind === "error" ? c.danger : c.fg,
          borderRadius: radius.md,
          paddingVertical: space.md,
          paddingHorizontal: space.lg,
        }}
      >
        <AppText variant="label" style={{ color: toast.kind === "error" ? "#fff" : c.bg, textAlign: "center" }}>
          {toast.message}
        </AppText>
      </View>
    </View>
  );
}

export const useToast = () => useContext(Ctx);
