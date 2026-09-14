//go:build tools

// Package tools pins build-time code generators so `go mod tidy` records their
// dependencies in go.sum, letting `togo generate` run `go run github.com/99designs/gqlgen`
// at the version pinned in go.mod. Excluded from normal builds by the build tag.
package tools

import (
	_ "github.com/99designs/gqlgen"
)
