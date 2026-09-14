.PHONY: dev generate migrate seed build test agent

AGENT_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

agent: ## Static cloudy-agent builds the control plane serves from bin/agent (CLOUDY_AGENT_DIST)
	mkdir -p bin/agent
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$(AGENT_VERSION)" -o bin/agent/cloudy-agent-linux-amd64 ./cmd/agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=$(AGENT_VERSION)" -o bin/agent/cloudy-agent-linux-arm64 ./cmd/agent
	echo $(AGENT_VERSION) > bin/agent/VERSION

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
