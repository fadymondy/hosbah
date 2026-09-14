.PHONY: dev generate migrate seed build test

dev: ## Run the API with reload
	togo serve

generate: ## sqlc + gqlgen + atlas + openapi
	togo generate

migrate: ## Apply migrations
	togo migrate

seed: ## Seed the database
	togo seed

build: ## Build the API binary
	go build -o bin/cloudy ./cmd/api

test: ## Run tests
	go test ./...
