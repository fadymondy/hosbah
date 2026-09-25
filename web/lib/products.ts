/** Contract types for SaaS products, instances, upgrades and keys (MH-576 D7;
 * internal/resources/product*.go, internal/api/products.go, internal/api/product_keys_public.go). */

export type ProductSourceKind = "app" | "recipe" | "repo" | ""

export type Product = {
  id: string
  team_id: string
  name: string
  title: string | null
  description: string | null
  source_kind: string
  source_ref: string | null
  repo_url: string
  branch: string
  runtime: string | null
  port: number
  healthcheck_path: string
  healthcheck_timeout: number
  resources_memory_mb: number | null
  resources_cpus: number | null
  domain_suffix: string | null
  env_defaults: string
  status: string
  created_by: string
  created_at: string
  updated_at: string
}

export type ProductInstance = {
  id: string
  team_id: string
  product_id: string
  app_id: string | null
  customer_ref: string
  name: string
  hostname: string | null
  database_id: string | null
  status: string
  created_by: string
  created_at: string
  updated_at: string
}

export type ProductKey = {
  id: string
  team_id: string
  product_id: string
  name: string
  webhook_url: string | null
  created_by: string
  last_used_at: string | null
  revoked_at: string | null
  created_at: string
  updated_at: string
}

export type ProductUpgrade = {
  id: string
  team_id: string
  product_id: string
  ref: string
  status: string
  total: number
  completed: number
  failed: number
  current_instance_id: string | null
  error: string | null
  created_by: string
  finished_at: string | null
  created_at: string
  updated_at: string
}

export type ProductDetail = {
  product: Product
  instances: ProductInstance[]
  keys: ProductKey[]
}

export type MintedProductKey = {
  key: ProductKey
  token: string
  webhook_secret?: string
}
