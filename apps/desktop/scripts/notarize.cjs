// Notarises the macOS build after electron-builder signs it.
//
// Driven by environment variables rather than `mac.notarize: true` so the outcome is never
// ambiguous: a machine with credentials notarises and says so, a machine without skips loudly and
// produces an honestly-unsigned build. The failure this avoids is the quiet one — a build that
// looks finished, installs on the machine that made it, and is refused by Gatekeeper on every
// other Mac with a message the user cannot act on.
//
// Needs, all from the Apple Developer account:
//   APPLE_ID                 the Apple ID that owns the Developer Program membership
//   APPLE_APP_SPECIFIC_PASSWORD  appleid.apple.com → Sign-In and Security → App-Specific Passwords
//   APPLE_TEAM_ID            the 10-character team id from developer.apple.com → Membership
//
// Signing itself is separate and comes from the keychain: a "Developer ID Application"
// certificate must be installed, or electron-builder signs nothing and this step has nothing
// valid to submit.
const { existsSync } = require("node:fs")
const { join } = require("node:path")

exports.default = async function notarizing(context) {
  const { electronPlatformName, appOutDir } = context
  if (electronPlatformName !== "darwin") return

  const appleId = process.env.APPLE_ID
  const appleIdPassword = process.env.APPLE_APP_SPECIFIC_PASSWORD
  const teamId = process.env.APPLE_TEAM_ID

  if (!appleId || !appleIdPassword || !teamId) {
    // Loud, and it names what is missing. A one-line "skipping" is how an unsigned build reaches
    // a release page.
    const missing = [
      !appleId && "APPLE_ID",
      !appleIdPassword && "APPLE_APP_SPECIFIC_PASSWORD",
      !teamId && "APPLE_TEAM_ID",
    ].filter(Boolean)
    console.warn(
      `\n[notarize] SKIPPED — missing ${missing.join(", ")}.\n` +
        `[notarize] This build is NOT notarised. Gatekeeper will refuse it on any Mac but this one.\n` +
        `[notarize] Do not publish it.\n`,
    )
    return
  }

  const appName = context.packager.appInfo.productFilename
  const appPath = join(appOutDir, `${appName}.app`)
  if (!existsSync(appPath)) throw new Error(`[notarize] no app at ${appPath}`)

  // Imported lazily: it is a macOS-only dependency and requiring it at module load would break
  // the Windows and Linux builds, which run this same afterSign hook.
  const { notarize } = require("@electron/notarize")

  console.log(`[notarize] submitting ${appName}.app — this takes a few minutes`)
  await notarize({ appPath, appleId, appleIdPassword, teamId })
  console.log(`[notarize] done`)
}
