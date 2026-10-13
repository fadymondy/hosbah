import { useRouter } from "expo-router";
import { ChevronLeft, ChevronRight } from "lucide-react-native";
import { I18nManager, Pressable, RefreshControl, ScrollView, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useColors, useT } from "@/lib/prefs";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";

interface Props {
  title: string;
  subtitle?: string;
  back?: boolean;
  right?: React.ReactNode;
  refreshing?: boolean;
  onRefresh?: () => void;
  /** Content that must not scroll away (e.g. a segmented control under the title). */
  header?: React.ReactNode;
  children: React.ReactNode;
  testID?: string;
}

export function Screen({ title, subtitle, back, right, refreshing, onRefresh, header, children, testID }: Props) {
  const c = useColors();
  const t = useT();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const BackIcon = I18nManager.isRTL ? ChevronRight : ChevronLeft;
  return (
    <View testID={testID} style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top }}>
      <View style={{ paddingHorizontal: space.lg, paddingTop: space.md, paddingBottom: space.sm, gap: space.sm }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: space.sm }}>
          {back ? (
            <Pressable
              testID="back"
              accessibilityRole="button"
              accessibilityLabel={t("common.back")}
              hitSlop={12}
              onPress={() => (router.canGoBack() ? router.back() : router.replace("/(tabs)"))}
            >
              <BackIcon size={24} color={c.fg} />
            </Pressable>
          ) : null}
          <View style={{ flex: 1 }}>
            <AppText variant="title" numberOfLines={1} accessibilityRole="header">
              {title}
            </AppText>
            {subtitle ? <AppText variant="caption">{subtitle}</AppText> : null}
          </View>
          {right}
        </View>
        {header}
      </View>
      <ScrollView
        contentContainerStyle={{ padding: space.lg, paddingBottom: insets.bottom + space.xxl, gap: space.md }}
        keyboardShouldPersistTaps="handled"
        refreshControl={onRefresh ? <RefreshControl refreshing={!!refreshing} onRefresh={onRefresh} tintColor={c.brand} /> : undefined}
      >
        {children}
      </ScrollView>
    </View>
  );
}
