module github.com/fadymondy/hosbah

go 1.26.4

require (
	github.com/99designs/gqlgen v0.17.66
	github.com/danielgtaylor/huma/v2 v2.27.0
	github.com/go-chi/chi/v5 v5.3.0
	github.com/togo-framework/togo v0.21.0
	// SQLite is the built-in default driver. Postgres/MySQL/Mongo drivers come from
	// their db-* PLUGIN (added to internal/plugins by `togo new --db`), which pulls
	// the raw driver transitively — so it isn't a direct dependency of this app.
	modernc.org/sqlite v1.53.0
)

require (
	filippo.io/edwards25519 v1.2.0
	github.com/BurntSushi/toml v1.6.0
	github.com/coder/websocket v1.8.15
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/jackc/pgx/v5 v5.7.1
	github.com/togo-framework/audit v0.1.1
	github.com/togo-framework/auth v0.8.0
	github.com/togo-framework/authz v0.1.1
	github.com/togo-framework/cache v0.3.0
	github.com/togo-framework/db-postgres v0.1.0
	github.com/togo-framework/faker v0.1.0
	github.com/togo-framework/i18n v0.2.0
	github.com/togo-framework/orm v0.1.0
	github.com/togo-framework/queue v0.3.0
	github.com/togo-framework/realtime v0.2.0
	github.com/togo-framework/storage v0.2.0
	github.com/togo-framework/validation v0.1.0
	github.com/vektah/gqlparser/v2 v2.5.22
	golang.org/x/crypto v0.28.0
	rsc.io/qr v0.2.0
)

require (
	github.com/agnivade/levenshtein v1.2.0 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.5 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/sosodev/duration v1.3.1 // indirect
	github.com/togo-framework/providers v0.1.0 // indirect
	github.com/togo-framework/settings v0.1.1 // indirect
	github.com/urfave/cli/v2 v2.27.5 // indirect
	github.com/xrash/smetrics v0.0.0-20240521201337-686a1a2994c1 // indirect
	golang.org/x/mod v0.36.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.44.0 // indirect
	golang.org/x/text v0.22.0 // indirect
	golang.org/x/tools v0.45.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.73.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
