/** Contract types for site templates (internal/api/site_templates.go, internal/sitetemplate) —
 * MH-576 B11: a declarative YAML document that scaffolds a ready-to-deploy app in one step. */

export type SiteTemplateDriver = {
  name: string
  label: string
  build: "railpack" | "dockerfile"
  preset: string
  runtime: string
  default_port: number
  default_health_path: string
  summary: string
}

export type SiteTemplate = {
  id: string
  name: string
  description: string
  driver: string
  source_yaml: string
  builtin: boolean
  created_by?: string
  created_at?: string
  updated_at?: string
}

export type RenderedEnv = {
  name: string
  value?: string
  secret: boolean
}

export type Rendered = {
  driver: SiteTemplateDriver
  repo_url: string
  branch: string
  port: number
  health_path: string
  env: RenderedEnv[]
  hosbah_toml?: string
  notes?: string
}

export type ServiceRefs = {
  postgres_service?: string
  mysql_service?: string
  redis_service?: string
}
