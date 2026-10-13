// Starts Metro with the fixture server (no network, no real account). Usage: npm run mock
import { spawnSync } from "node:child_process";

const env = { ...process.env, EXPO_PUBLIC_MOCK: "1" };
if (process.argv.includes("--signed-in")) env.EXPO_PUBLIC_MOCK_SIGNED_IN = "1";
spawnSync("adb", ["reverse", "tcp:8081", "tcp:8081"], { stdio: "inherit", shell: true });
const r = spawnSync("npx", ["expo", "start", "--dev-client", "--clear"], { stdio: "inherit", env, shell: true });
process.exit(r.status ?? 0);
