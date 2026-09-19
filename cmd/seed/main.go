// Command seed populates the database with sample data via the generated seeders.
// Run with `togo seed`.
package main

import (
	"context"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/hosbah/internal/app"
	"github.com/fadymondy/hosbah/internal/db/seeders"
)

func main() {
	k := togo.New()
	ctx := context.Background()
	a := app.New(ctx, k)
	defer k.Close()

	if a.SQLDB == nil {
		k.Log.Error("seed: database unavailable")
		return
	}
	if err := seeders.SeedAll(ctx, a); err != nil {
		k.Log.Error("seed failed", "err", err)
		return
	}
	k.Log.Info("seed complete")
}
