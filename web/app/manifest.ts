import type { MetadataRoute } from "next"

/** The installable app (PWA): /manifest.webmanifest. Icons come from scripts/make-icons.mjs. */
export default function manifest(): MetadataRoute.Manifest {
  return {
    id: "/",
    name: "Hosbah — حوسبة",
    short_name: "Hosbah",
    description: "Servers and apps, managed. Proxmox, Docker and bring-your-own servers.",
    start_url: "/",
    scope: "/",
    display: "standalone",
    background_color: "#0b1429",
    theme_color: "#0b1429",
    lang: "en",
    dir: "auto",
    categories: ["developer", "productivity", "utilities"],
    icons: [
      { src: "/icons/icon-192.png", sizes: "192x192", type: "image/png", purpose: "any" },
      { src: "/icons/icon-512.png", sizes: "512x512", type: "image/png", purpose: "any" },
      { src: "/icons/icon-maskable-512.png", sizes: "512x512", type: "image/png", purpose: "maskable" },
    ],
    shortcuts: [
      { name: "Servers", short_name: "Servers", url: "/en" },
      { name: "الخوادم", short_name: "الخوادم", url: "/ar" },
    ],
  }
}
