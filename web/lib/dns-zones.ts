/** Contract types for the DNS zone manager (MH-578 E8; internal/api/dns_zones.go). */

export type DNSZoneRecordType = "A" | "AAAA" | "CNAME" | "TXT" | "MX" | "SRV" | "CAA"

export const DNS_ZONE_RECORD_TYPES: DNSZoneRecordType[] = ["A", "AAAA", "CNAME", "TXT", "MX", "SRV", "CAA"]

export type DNSManagedKind = "app_domain" | "proxy_host" | "mail"

export type DNSZone = {
  id: string
  name: string
  status: string
  credential_id: string
  credential_name: string
}

export type DNSZoneRecord = {
  id: string
  type: DNSZoneRecordType
  name: string
  content: string
  ttl: number
  priority?: number
  proxied: boolean
  managed_by_hosbah: boolean
  managed_kind?: DNSManagedKind
}

export type DNSZoneRecordInput = {
  type: DNSZoneRecordType
  name: string
  content: string
  ttl?: number
  priority?: number
  proxied?: boolean
}
