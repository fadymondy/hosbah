import { Tabs } from "expo-router";
import type { ColorValue } from "react-native";
import { Bell, Globe, LayoutDashboard, Rocket, Settings } from "lucide-react-native";

import { fontFor, usePrefs } from "@/lib/prefs";
import { useNotifications } from "@/lib/queries";

export default function TabsLayout() {
  const { colors: c, t, locale } = usePrefs();
  const unread = useNotifications().data?.unread ?? 0;
  const icon = (Icon: typeof Bell) =>
    function TabIcon({ color, size }: { color: ColorValue; size: number }) {
      return <Icon color={color as string} size={size} />;
    };
  return (
    <Tabs
      screenOptions={{
        headerShown: false,
        tabBarActiveTintColor: c.brand,
        tabBarInactiveTintColor: c.muted,
        tabBarStyle: { backgroundColor: c.card, borderTopColor: c.line },
        tabBarLabelStyle: { fontFamily: fontFor(locale, "medium"), fontSize: 11 },
        sceneStyle: { backgroundColor: c.bg },
      }}
    >
      <Tabs.Screen name="index" options={{ title: t("tab.overview"), tabBarIcon: icon(LayoutDashboard), tabBarButtonTestID: "tab-overview" }} />
      <Tabs.Screen name="apps" options={{ title: t("tab.apps"), tabBarIcon: icon(Rocket), tabBarButtonTestID: "tab-apps" }} />
      <Tabs.Screen name="infra" options={{ title: t("tab.domains"), tabBarIcon: icon(Globe), tabBarButtonTestID: "tab-domains" }} />
      <Tabs.Screen
        name="alerts"
        options={{
          title: t("tab.alerts"),
          tabBarIcon: icon(Bell),
          tabBarButtonTestID: "tab-alerts",
          ...(unread > 0 ? { tabBarBadge: unread > 99 ? "99+" : unread, tabBarBadgeStyle: { backgroundColor: c.danger, color: "#fff" } } : {}),
        }}
      />
      <Tabs.Screen name="settings" options={{ title: t("tab.settings"), tabBarIcon: icon(Settings), tabBarButtonTestID: "tab-settings" }} />
    </Tabs>
  );
}
