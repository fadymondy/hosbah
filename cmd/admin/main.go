// Command admin is the operator's bootstrap tool — for things that must exist before anyone
// can use the dashboard. Run on the server, with DATABASE_URL access:
//
//	go run ./cmd/admin promote <email>              grant platform admin (takes effect at next sign-in)
//	go run ./cmd/admin demote <email>               remove platform admin
//	go run ./cmd/admin mint-admin-token <email> [name]   print a 30-day admin MCP token, once
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/cloudy/internal/config"
	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/tenancy"
	"github.com/fadymondy/cloudy/internal/tokens"
)

func main() {
	config.LoadDotEnv()
	if len(os.Args) < 3 {
		fail("usage: admin promote|demote|mint-admin-token <email> [name]")
	}
	sqlDB, err := sql.Open("pgx", togo.LoadConfig().DatabaseURL)
	if err != nil {
		fail("open: %v", err)
	}
	defer sqlDB.Close()
	ctx := context.Background()
	q := db.New(sqlDB)

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

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "admin: "+format+"\n", args...)
	os.Exit(1)
}
