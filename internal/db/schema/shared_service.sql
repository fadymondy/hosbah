-- Hosbah: a shared service is a service Hosbah found on a Proxmox guest of a node server, that is
-- not itself enrolled as a Hosbah server or a managed/imported data service — e.g. the node's own
-- shared Postgres, Redis, NATS or MinIO container that many apps on other guests point at. It is
-- not a data_service: most kinds here (search, scraping, browser) are not databases, and Hosbah
-- holds no credential and does no lifecycle (create/backup/restore) for any of them — it is purely
-- a directory entry apps can link to, kept in its own table rather than stretching data_services'
-- CHECK(kind IN postgres, mysql, redis, minio, mongodb) and 'managed'/'imported' lifecycle meaning
-- onto guests Hosbah does not and will not manage.
CREATE TABLE shared_services (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    team_id text NOT NULL,
    server_id text NOT NULL,
    guest_vmid text NOT NULL,
    guest_type text NOT NULL,
    kind text NOT NULL,
    name text NOT NULL,
    host text,
    port bigint,
    hidden boolean NOT NULL DEFAULT false,
    name_manual boolean NOT NULL DEFAULT false,
    kind_manual boolean NOT NULL DEFAULT false,
    last_seen_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- kind: postgres, mysql, redis, nats, minio, searxng, firecrawl, browser, meilisearch, rabbitmq,
-- elasticsearch, opensearch, clickhouse, mongodb, other. name/kind can be renamed or recategorised
-- by a member (name_manual/kind_manual then true) and the scan never overwrites that; hidden lets a
-- member drop a guest that is not actually a shared service from the list without losing the scan.
ALTER TABLE shared_services ADD CONSTRAINT shared_services_kind_check CHECK (kind IN ('postgres', 'mysql', 'redis', 'nats', 'minio', 'searxng', 'firecrawl', 'browser', 'meilisearch', 'rabbitmq', 'elasticsearch', 'opensearch', 'clickhouse', 'mongodb', 'other'));
ALTER TABLE shared_services ADD CONSTRAINT shared_services_guest_type_check CHECK (guest_type IN ('lxc', 'qemu'));
ALTER TABLE shared_services ADD CONSTRAINT shared_services_port_check CHECK (port IS NULL OR port BETWEEN 1 AND 65535);
CREATE UNIQUE INDEX shared_services_server_guest_key ON shared_services (server_id, guest_vmid);
CREATE UNIQUE INDEX shared_services_id_team_key ON shared_services (id, team_id);
CREATE INDEX shared_services_team_idx ON shared_services (team_id);
