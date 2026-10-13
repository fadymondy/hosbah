import { Bell } from "lucide-react-native";
import { useEffect } from "react";
import { Pressable, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { dispatchLink } from "@/lib/links";
import { dismissBanner, useBanner } from "@/lib/push";
import { useColors } from "@/lib/prefs";
import { AppText } from "@/ui/text";
import { radius, space } from "@/ui/theme";

/** The in-app banner for a push that arrives while the app is open (the OS shows nothing in that case). */
export function BannerHost() {
  const banner = useBanner();
  const c = useColors();
  const insets = useSafeAreaInsets();
  useEffect(() => {
    if (!banner) return;
    const h = setTimeout(dismissBanner, 6000);
    return () => clearTimeout(h);
  }, [banner]);
  if (!banner) return null;
  return (
    <View pointerEvents="box-none" style={{ position: "absolute", top: insets.top + space.sm, left: space.md, right: space.md }}>
      <Pressable
        testID="push-banner"
        accessibilityRole="alert"
        onPress={() => {
          if (banner.dest) dispatchLink(banner.dest);
          dismissBanner();
        }}
        style={{
          flexDirection: "row",
          gap: space.md,
          alignItems: "center",
          backgroundColor: c.elevated,
          borderColor: c.brand,
          borderWidth: 1,
          borderRadius: radius.lg,
          padding: space.md,
          elevation: 8,
        }}
      >
        <Bell size={20} color={c.brand} />
        <View style={{ flex: 1 }}>
          <AppText variant="label" numberOfLines={1}>
            {banner.title}
          </AppText>
          {banner.body ? (
            <AppText variant="caption" numberOfLines={2}>
              {banner.body}
            </AppText>
          ) : null}
        </View>
      </Pressable>
    </View>
  );
}
