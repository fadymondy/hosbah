package services

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ConnEndpoint is all that is ever extracted from an env value: its connection endpoint. The
// value itself (user, password, path, query) never leaves ParseConnVar — callers must never log or
// return the raw value.
type ConnEndpoint struct {
	Scheme string
	Host   string // hostname or IP, no port
	Port   int    // 0 when the value had none
}

// connVarNames are env var names recognised as connection variables even when their value has no
// scheme (a bare host:port). Matched case-insensitively; a var ending in one of these, or exactly
// equal to one, counts (so DATABASE_URL, APP_DATABASE_URL, PG_URL, POSTGRES_URL, REDIS_URL,
// NATS_URL, S3_ENDPOINT, MINIO_ENDPOINT, SEARXNG_URL all match).
var connVarSuffixes = []string{
	"DATABASE_URL", "POSTGRES_URL", "PG_URL", "PGURL", "REDIS_URL", "NATS_URL",
	"S3_ENDPOINT", "MINIO_ENDPOINT", "SEARXNG_URL", "MONGO_URL", "MONGODB_URL", "AMQP_URL",
	"RABBITMQ_URL", "ELASTICSEARCH_URL", "OPENSEARCH_URL", "CLICKHOUSE_URL", "MEILISEARCH_URL",
	"BROWSER_URL", "FIRECRAWL_URL",
}

// LooksLikeConnVar says whether name is a connection variable by convention (regardless of what
// its value turns out to parse as): *_DATABASE_URL, PG*, REDIS_URL, and the rest of
// connVarSuffixes. Pure, case-insensitive.
func LooksLikeConnVar(name string) bool {
	u := strings.ToUpper(strings.TrimSpace(name))
	if strings.HasPrefix(u, "PG") {
		return true
	}
	for _, suf := range connVarSuffixes {
		if u == suf || strings.HasSuffix(u, "_"+suf) {
			return true
		}
	}
	return false
}

var hostPort = regexp.MustCompile(`^[a-zA-Z0-9_.-]+:[0-9]{1,5}$`)

// ParseConnVar extracts the connection endpoint out of an env value, or ok=false when the value is
// not a URL and not a bare host:port. Nothing but scheme/host/port is ever returned — the caller
// must never pass value on to a log or a response after calling this.
func ParseConnVar(value string) (ep ConnEndpoint, ok bool) {
	v := strings.TrimSpace(value)
	if v == "" {
		return ConnEndpoint{}, false
	}
	if strings.Contains(v, "://") {
		u, err := url.Parse(v)
		if err != nil || u.Hostname() == "" {
			return ConnEndpoint{}, false
		}
		port, _ := strconv.Atoi(u.Port())
		return ConnEndpoint{Scheme: u.Scheme, Host: u.Hostname(), Port: port}, true
	}
	if hostPort.MatchString(v) {
		host, portS, found := strings.Cut(v, ":")
		if !found {
			return ConnEndpoint{}, false
		}
		port, err := strconv.Atoi(portS)
		if err != nil || port < 1 || port > 65535 {
			return ConnEndpoint{}, false
		}
		return ConnEndpoint{Host: host, Port: port}, true
	}
	return ConnEndpoint{}, false
}
