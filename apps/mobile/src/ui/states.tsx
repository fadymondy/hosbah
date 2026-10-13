import { ActivityIndicator, View } from "react-native";

import { useT } from "@/lib/prefs";
import { errorKey } from "@/lib/errors";
import { Button } from "@/ui/button";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";

export function Loading() {
  const t = useT();
  return (
    <View style={{ padding: space.xxl, alignItems: "center" }} accessibilityLabel={t("common.loading")}>
      <ActivityIndicator />
    </View>
  );
}

export function EmptyState({ text, testID }: { text: string; testID?: string }) {
  return (
    <View testID={testID} style={{ padding: space.xxl, alignItems: "center" }}>
      <AppText tone="muted" style={{ textAlign: "center" }}>
        {text}
      </AppText>
    </View>
  );
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const t = useT();
  return (
    <View testID="error-state" style={{ padding: space.xl, alignItems: "center", gap: space.md }}>
      <AppText tone="dangerText" style={{ textAlign: "center" }}>
        {t(errorKey(error))}
      </AppText>
      {onRetry ? <Button kind="secondary" label={t("common.retry")} onPress={onRetry} /> : null}
    </View>
  );
}

/** Loading / error / empty / content for a query, in that order of precedence. */
export function QueryBoundary({
  loading,
  error,
  empty,
  emptyText,
  onRetry,
  hasData,
  children,
}: {
  loading: boolean;
  error: unknown;
  empty?: boolean;
  emptyText?: string;
  onRetry?: () => void;
  /** Cached data is shown even when a refetch failed (offline). */
  hasData: boolean;
  children: React.ReactNode;
}) {
  if (loading && !hasData) return <Loading />;
  if (error && !hasData) return <ErrorState error={error} {...(onRetry ? { onRetry } : {})} />;
  if (empty && emptyText) return <EmptyState text={emptyText} />;
  return <>{children}</>;
}
