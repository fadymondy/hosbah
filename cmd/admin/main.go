// Command admin is the operator's bootstrap tool — for things that must exist before anyone
// can use the dashboard. Run on the server, with DATABASE_URL access:
//
//	go run ./cmd/admin promote <email>              grant platform admin (takes effect at next sign-in)
//	go run ./cmd/admin demote <email>               remove platform admin
//	go run ./cmd/admin mint-admin-token <email> [name]   print a 30-day admin MCP token, once
//	go run ./cmd/admin oauth-client add <name> <redirect_uri>...   register an OAuth client (MH-1153);
//	                                                               prints client_id and the secret, once
//	go run ./cmd/admin oauth-client list | revoke <client_id>
package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/hosbah/internal/config"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

func main() {
	config.LoadDotEnv()
	if len(os.Args) < 3 {
		fail("usage: admin promote|demote|mint-admin-token <email> [name] | oauth-client add|list|revoke ...")
	}
	sqlDB, err := sql.Open("pgx", togo.LoadConfig().DatabaseURL)
	if err != nil {
		fail("open: %v", err)
	}
	defer sqlDB.Close()
	ctx := context.Background()
	q := db.New(sqlDB)

	if os.Args[1] == "oauth-client" {
		oauthClient(ctx, sqlDB, os.Args[2:])
		return
	}

	u, err := q.UserByEmail(ctx, os.Args[2])
	if err != nil {
		fail("no account with email %s (sign up first)", os.Args[2])
	}

	switch os.Args[1] {
	case "promote", "demote":
		var roles []string
		for _, r := range strings.Split(u.Roles, ",") {
			if r = strings.TrimSpace(r); r != "" && r != "admin" {
				roles = append(roles, r)
			}
		}
		if os.Args[1] == "promote" {
			roles = append(roles, "admin")
		}
		if err := q.SetUserRoles(ctx, db.SetUserRolesParams{ID: u.ID, Roles: strings.Join(roles, ",")}); err != nil {
			fail("update: %v", err)
		}
		fmt.Printf("%s roles: %q — sign out and in again for the session to carry it\n", u.Email, strings.Join(roles, ","))
	case "mint-admin-token":
		if !strings.Contains(","+u.Roles+",", ",admin,") {
			fail("%s is not a platform admin (run promote first)", u.Email)
		}
		name := "cli"
		if len(os.Args) > 3 {
			name = os.Args[3]
		}
		plain, hash := tokens.New(tokens.PrefixAdminMCP)
		exp := time.Now().Add(30 * 24 * time.Hour).UTC()
		err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{UserID: u.ID, PlatformAdmin: true}, func(q *db.Queries, _ *sql.Tx) error {
			_, err := q.InsertAdminMcpToken(ctx, db.InsertAdminMcpTokenParams{UserID: u.ID, Name: name, TokenHash: hash, ExpiresAt: &exp})
			return err
		})
		if err != nil {
			fail("insert: %v", err)
		}
		fmt.Println(plain)
	default:
		fail("unknown command %q", os.Args[1])
	}
}

// oauthClient manages the OAuth authorization server's clients (internal/api/oauth.go).
func oauthClient(ctx context.Context, sqlDB *sql.DB, args []string) {
	err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{PlatformAdmin: true}, func(q *db.Queries, _ *sql.Tx) error {
		switch args[0] {
		case "add":
			if len(args) < 3 {
				fail("usage: admin oauth-client add <name> <redirect_uri>...")
			}
			for _, u := range args[2:] {
				p, err := url.Parse(u)
				if err != nil || p.Fragment != "" || (p.Scheme != "https" && !(p.Scheme == "http" && (p.Hostname() == "localhost" || p.Hostname() == "127.0.0.1"))) {
					fail("%s is not a valid redirect URI (https, or http on localhost; no fragment)", u)
				}
			}
			clientID, _ := tokens.New("hbc_")
			secret, hash := tokens.New(tokens.PrefixOAuthSecret)
			if _, err := q.InsertOAuthClient(ctx, db.InsertOAuthClientParams{ClientID: clientID, Name: args[1], SecretHash: &hash,
				RedirectUris: strings.Join(args[2:], "\n")}); err != nil {
				return err
			}
			fmt.Printf("client_id:     %s\nclient_secret: %s\n(the secret is shown once; only its hash is kept)\n", clientID, secret)
		case "list":
			rows, err := q.ListOAuthClients(ctx)
			if err != nil {
				return err
			}
			for _, c := range rows {
				state := "active"
				if c.RevokedAt != nil {
					state = "revoked"
				}
				fmt.Printf("%s  %s  %s  %s\n", c.ClientID, state, c.Name, strings.ReplaceAll(c.RedirectUris, "\n", " "))
			}
		case "revoke":
			if len(args) < 2 {
				fail("usage: admin oauth-client revoke <client_id>")
			}
			n, err := q.RevokeOAuthClient(ctx, args[1])
			if err != nil {
				return err
			}
			if n == 0 {
				fail("no active client %s", args[1])
			}
			fmt.Println("revoked; its app tokens stay until revoked on each app's settings")
		default:
			fail("usage: admin oauth-client add|list|revoke")
		}
		return nil
	})
	if err != nil {
		fail("oauth-client: %v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "admin: "+format+"\n", args...)
	os.Exit(1)
}
