import type { Palette } from "@hosbah/client/brand";
import { Text, type TextProps, type TextStyle } from "react-native";

import { fontFor, usePrefs } from "@/lib/prefs";

export type TextVariant = "title" | "heading" | "body" | "label" | "caption" | "mono";
export type TextTone = keyof Pick<Palette, "fg" | "body" | "muted" | "brand" | "gold" | "okText" | "warnText" | "dangerText" | "onAction">;

interface Props extends TextProps {
  variant?: TextVariant;
  tone?: TextTone;
}

const SIZES: Record<TextVariant, { size: number; weight: "regular" | "medium" | "strong" | "mono"; line: number }> = {
  title: { size: 24, weight: "strong", line: 34 },
  heading: { size: 17, weight: "strong", line: 26 },
  body: { size: 15, weight: "regular", line: 23 },
  label: { size: 14, weight: "medium", line: 21 },
  caption: { size: 12, weight: "regular", line: 18 },
  mono: { size: 12, weight: "mono", line: 18 },
};

export function AppText({ variant = "body", tone, style, ...rest }: Props) {
  const { locale, colors } = usePrefs();
  const s = SIZES[variant];
  const toneName: TextTone = tone ?? (variant === "caption" ? "muted" : variant === "title" || variant === "heading" ? "fg" : "body");
  const base: TextStyle = { fontFamily: fontFor(locale, s.weight), fontSize: s.size, lineHeight: s.line, color: colors[toneName] };
  return <Text {...rest} style={[base, style]} />;
}
