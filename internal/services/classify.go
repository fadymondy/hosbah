// Package services classifies Proxmox guests as shared infrastructure ("shared services": a node's
// shared Postgres, Redis, NATS, MinIO, SearXNG, Firecrawl, a headless-browser box, and so on), keeps
// a per-team directory of them (internal/services/sync.go), and links apps to the shared and
// managed/imported data services their env variables point at (internal/services/link.go).
package services

import (
	"regexp"
	"strings"
)

// Kinds a guest can classify as. "other" means the guest looked like shared infra (matched no
// database token importer.DBKindOfGuestName already claims) but not a known kind — never returned
// by Classify; a caller ignoring an unmatched guest is expected.
const (
	KindPostgres      = "postgres"
	KindMySQL         = "mysql"
	KindRedis         = "redis"
	KindNATS          = "nats"
	KindMinio         = "minio"
	KindSearXNG       = "searxng"
	KindFirecrawl     = "firecrawl"
	KindBrowser       = "browser"
	KindMeilisearch   = "meilisearch"
	KindRabbitMQ      = "rabbitmq"
	KindElasticsearch = "elasticsearch"
	KindOpensearch    = "opensearch"
	KindClickhouse    = "clickhouse"
	KindMongo         = "mongodb"
	KindOther         = "other"
)

// DefaultPort is each kind's well-known port.
var DefaultPort = map[string]int{
	KindPostgres:      5432,
	KindMySQL:         3306,
	KindRedis:         6379,
	KindNATS:          4222,
	KindMinio:         9000,
	KindSearXNG:       8080,
	KindFirecrawl:     3002,
	KindBrowser:       9222,
	KindMeilisearch:   7700,
	KindRabbitMQ:      5672,
	KindElasticsearch: 9200,
	KindOpensearch:    9200,
	KindClickhouse:    8123,
	KindMongo:         27017,
}

// tokens maps a whole name token (split on anything but letters/digits) to a kind. Checked against
// name, tags and hostname alike. Superset of importer.guestDBTokens (kept independent: this package
// must not import internal/importer to avoid a cycle with internal/ops).
var tokens = map[string]string{
	"pg": KindPostgres, "postgres": KindPostgres, "postgresql": KindPostgres, "psql": KindPostgres,
	"pgsql": KindPostgres, "postgis": KindPostgres, "timescale": KindPostgres, "timescaledb": KindPostgres,
	"mysql": KindMySQL, "maria": KindMySQL, "mariadb": KindMySQL,
	"redis": KindRedis, "valkey": KindRedis,
	"nats": KindNATS,
	"minio": KindMinio, "s3": KindMinio,
	"searxng": KindSearXNG, "searx": KindSearXNG, "search": KindSearXNG,
	"firecrawl": KindFirecrawl, "crawl": KindFirecrawl, "crawler": KindFirecrawl,
	"chrome": KindBrowser, "chromium": KindBrowser, "chromebox": KindBrowser, "browser": KindBrowser, "browserless": KindBrowser, "puppeteer": KindBrowser, "playwright": KindBrowser,
	"meilisearch": KindMeilisearch, "meili": KindMeilisearch,
	"rabbitmq": KindRabbitMQ, "rabbit": KindRabbitMQ, "amqp": KindRabbitMQ,
	"elasticsearch": KindElasticsearch, "elastic": KindElasticsearch,
	"opensearch": KindOpensearch,
	"clickhouse": KindClickhouse, "ch": KindClickhouse,
	"mongo": KindMongo, "mongodb": KindMongo,
}

var tokenSplit = regexp.MustCompile(`[^a-z0-9]+`)

// Classify maps a guest's name, tags (space or comma separated) and hostname to a shared-service
// kind: the first whole token (split on anything but letters/digits) of any of the three that names
// a known kind wins, name checked first, then tags, then hostname. "" means no known kind. Pure.
func Classify(name, tags, hostname string) string {
	for _, s := range []string{name, tags, hostname} {
		for _, t := range tokenSplit.Split(strings.ToLower(s), -1) {
			if k, ok := tokens[t]; ok {
				return k
			}
		}
	}
	return ""
}
