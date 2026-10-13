import type { Palette } from "@hosbah/client/brand";
import { useMemo } from "react";
import { StyleSheet } from "react-native";

import { useColors } from "@/lib/prefs";

export const space = { xs: 4, sm: 8, md: 12, lg: 16, xl: 24, xxl: 32 } as const;
export const radius = { sm: 8, md: 12, lg: 16, pill: 999 } as const;

/** StyleSheet.create with the current palette; rebuilt only when the theme changes. */
export function useStyles<T extends StyleSheet.NamedStyles<T>>(factory: (c: Palette) => T): T {
  const c = useColors();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  return useMemo(() => StyleSheet.create(factory(c)), [c]);
}
