import type { ReactNode } from "react"

/**
 * Settings pages (General, Notifications, Status pages, Log drains, MCP tokens, SSH keys,
 * Credentials, Certificates, Audit log, Billing). The sidebar's Settings group is their navigation;
 * the URLs are the old ones (/tokens, /audit, /costs…), so links and bookmarks keep working.
 */
export default function SettingsLayout({ children }: { children: ReactNode }) {
  return <div className="flex flex-col">{children}</div>
}
