/** Contract types for team mail servers (internal/api/mail.go, internal/mail) — MH-577 I1-I3. */

export type MailServerStatus = "new" | "installing" | "running" | "failed" | "removing"

export type MailServer = {
  id: string
  server_id: string
  hostname: string
  admin_port: number
  status: MailServerStatus
  error: string | null
}

export type MailDomainStatus = "new" | "creating" | "active" | "failed" | "removing"

export type MailDomain = {
  id: string
  mail_server_id: string
  domain: string
  dkim_selector: string | null
  dkim_public_key: string | null
  status: MailDomainStatus
  error: string | null
}

export type MailboxStatus = "new" | "creating" | "ready" | "failed" | "removing"

export type Mailbox = {
  id: string
  mail_domain_id: string
  local_part: string
  email: string
  quota_mb: number
  status: MailboxStatus
  error: string | null
}

export type MailboxCreated = {
  mailbox: Mailbox
  password: string
}

export type MailAliasKind = "alias" | "forward"

export type MailAlias = {
  id: string
  mail_domain_id: string
  local_part: string
  email: string
  kind: MailAliasKind
  destinations: string[]
  status: MailboxStatus
  error: string | null
}

export type DnsRecordType = "MX" | "TXT"
export type DnsRecordPurpose = "mx" | "spf" | "dkim" | "dmarc"
export type DnsRecordStatus = "unknown" | "ok" | "missing" | "mismatch"

export type MailDnsRecord = {
  type: DnsRecordType
  name: string
  value: string
  priority?: number
  purpose: DnsRecordPurpose
  status: DnsRecordStatus
  found?: string
}

export type MailPtrCheck = {
  server_ip?: string
  hostname: string
  status: DnsRecordStatus
  found?: string
}

export type MailDomainDns = {
  records: MailDnsRecord[]
  ptr: MailPtrCheck
}

export type MailDnsPublishResult = {
  published: string[]
  skipped?: string[]
  zone?: string
}

/** MH-577 I5: a mail domain's deliverability report. */
export type BlacklistStatus = "ok" | "listed" | "error"

export type BlacklistCheck = {
  zone: string
  status: BlacklistStatus
  detail?: string
}

export type DeliverabilityReport = {
  domain: string
  dns: MailDomainDns
  blacklists: BlacklistCheck[]
  spam_filter_note: string
}

/** MH-577 I6: an SMTP relay credential dedicated to one app. */
export type MailRelay = {
  id: string
  mail_domain_id: string
  app_id: string
  local_part: string
  email: string
  attached: boolean
  status: MailboxStatus
  error: string | null
}

export type MailRelayCreated = {
  relay: MailRelay
  password: string
}

export function blacklistStatusVariant(status: BlacklistStatus): "success" | "neutral" | "danger" | "outline" {
  if (status === "ok") return "success"
  if (status === "listed") return "danger"
  return "outline"
}

export function mailStatusVariant(status: string): "success" | "neutral" | "danger" | "outline" {
  if (status === "running" || status === "active" || status === "ready") return "success"
  if (status === "failed") return "danger"
  if (status === "new" || status === "installing" || status === "creating" || status === "removing") return "neutral"
  return "outline"
}

export function dnsStatusVariant(status: DnsRecordStatus): "success" | "neutral" | "danger" | "outline" {
  if (status === "ok") return "success"
  if (status === "missing" || status === "mismatch") return "danger"
  return "outline"
}
