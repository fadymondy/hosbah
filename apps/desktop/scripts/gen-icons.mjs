// Refreshes build/icon.png (used by electron-builder for .ico / .icns) from the shared brand asset.
import { copyFileSync } from "node:fs";
copyFileSync(new URL("../../shared/assets/icon-512.png", import.meta.url), new URL("../build/icon.png", import.meta.url));
console.log("build/icon.png updated");
