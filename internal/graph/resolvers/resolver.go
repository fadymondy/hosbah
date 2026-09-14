package resolvers

import (
	"context"

	"github.com/fadymondy/cloudy/internal/app"
	graphgen "github.com/fadymondy/cloudy/internal/graph/gen"
)

// Resolver is the GraphQL root, holding the app container (DB, hooks, log).
type Resolver struct {
	App *app.App
}

func (r *Resolver) Query() graphgen.QueryResolver       { return &queryResolver{r} }
func (r *Resolver) Mutation() graphgen.MutationResolver { return &mutationResolver{r} }

type queryResolver struct{ *Resolver }
type mutationResolver struct{ *Resolver }

func (r *queryResolver) Health(ctx context.Context) (string, error) { return "ok", nil }
func (r *mutationResolver) Noop(ctx context.Context) (bool, error)  { return true, nil }
