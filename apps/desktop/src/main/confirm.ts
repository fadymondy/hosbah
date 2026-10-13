// The extra "are you sure" before an app action. macOS asks for Touch ID (or the password) through
// systemPreferences.promptTouchID when the Mac has it; everywhere else the renderer already made the
// person type the app's name, so there is nothing more to ask here.
import { systemPreferences } from "electron";

export function touchIdAvailable(): boolean {
  if (process.platform !== "darwin" || process.env.HOSBAH_E2E === "1") return false;
  try {
    return systemPreferences.canPromptTouchID();
  } catch {
    return false;
  }
}

/** True when the action may go ahead. A cancelled prompt rejects, which means "no". */
export async function authorize(reason: string): Promise<boolean> {
  if (!touchIdAvailable()) return true;
  try {
    await systemPreferences.promptTouchID(reason);
    return true;
  } catch {
    return false;
  }
}
