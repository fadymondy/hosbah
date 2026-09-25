package services

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		name, tags, hostname, want string
	}{
		{"pg-shared", "", "", KindPostgres},
		{"postgres-main", "", "", KindPostgres},
		{"redis", "", "", KindRedis},
		{"valkey-01", "", "", KindRedis},
		{"nats", "", "", KindNATS},
		{"minio", "", "", KindMinio},
		{"searxng", "", "", KindSearXNG},
		{"firecrawl", "", "", KindFirecrawl},
		{"chromebox", "", "", KindBrowser},
		{"meilisearch-01", "", "", KindMeilisearch},
		{"rabbitmq", "", "", KindRabbitMQ},
		{"elastic-01", "", "", KindElasticsearch},
		{"opensearch", "", "", KindOpensearch},
		{"clickhouse-analytics", "", "", KindClickhouse},
		{"mongo-01", "", "", KindMongo},
		{"random-vm", "", "", ""},
		{"webapp01", "", "", ""},
		// tags and hostname win when name doesn't classify
		{"guest-01", "postgres,shared", "", KindPostgres},
		{"guest-02", "", "redis.internal", KindRedis},
		// mysql/maria
		{"mysql-shared", "", "", KindMySQL},
		{"mariadb-01", "", "", KindMySQL},
	}
	for _, c := range cases {
		if got := Classify(c.name, c.tags, c.hostname); got != c.want {
			t.Errorf("Classify(%q,%q,%q) = %q, want %q", c.name, c.tags, c.hostname, got, c.want)
		}
	}
}
