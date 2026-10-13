// Brand tokens, mirrored from web/app/grid/grid-tokens.css and brand-cloudy.css.
export interface Palette {
  brand: string;
  action: string;
  onAction: string;
  bg: string;
  card: string;
  elevated: string;
  soft: string;
  line: string;
  fg: string;
  body: string;
  muted: string;
  gold: string;
  ok: string;
  warn: string;
  danger: string;
  okText: string;
  warnText: string;
  dangerText: string;
}

export const light: Palette = {
  brand: "#2e6f9e",
  action: "#2e6f9e",
  onAction: "#ffffff",
  bg: "#f0ebe1",
  card: "#f7f4ec",
  elevated: "#faf8f3",
  soft: "#ede7db",
  line: "#ded5c4",
  fg: "#0e1a3c",
  body: "#4a4438",
  muted: "#6e6551",
  gold: "#c9a227",
  ok: "#4e9a3e",
  warn: "#c9a227",
  danger: "#d9455f",
  okText: "#33702a",
  warnText: "#7d6310",
  dangerText: "#a8233b",
};

export const dark: Palette = {
  brand: "#7fb0d6",
  action: "#7fb0d6",
  onAction: "#0e1a3c",
  bg: "#0b1429",
  card: "#0e1a3c",
  elevated: "#18264a",
  soft: "#1a2747",
  line: "#25355c",
  fg: "#f0ebe1",
  body: "#c8d0e4",
  muted: "#8a97b8",
  gold: "#c9a227",
  ok: "#4e9a3e",
  warn: "#c9a227",
  danger: "#d9455f",
  okText: "#6fbf5e",
  warnText: "#c9a227",
  dangerText: "#e8788c",
};

export const palettes = { light, dark } as const;

/** Overall status colours (tray icon, header dot). */
export const statusColors = { green: "#4e9a3e", amber: "#c9a227", red: "#d9455f" } as const;

export const fonts = {
  ar: "Lusail",
  en: "Inter",
} as const;

export const assets = {
  mark: "assets/mark.svg",
  icon192: "assets/icon-192.png",
  icon512: "assets/icon-512.png",
  iconMaskable512: "assets/icon-maskable-512.png",
  lusailLight: "assets/Lusail-Light.woff2",
  lusailRegular: "assets/Lusail-Regular.woff2",
  lusailMedium: "assets/Lusail-Medium.woff2",
} as const;

export const brandName = { ar: "حوسبة", en: "Hosbah" } as const;
