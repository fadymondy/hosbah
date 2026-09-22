.PHONY: dev generate migrate seed build test agent cli

AGENT_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

agent: ## Static hosbah-agent builds the control plane serves from bin/agent (HOSBAH_AGENT_DIST)
	mkdir -p bin/agent
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$(AGENT_VERSION)" -o bin/agent/hosbah-agent-linux-amd64 ./cmd/agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=$(AGENT_VERSION)" -o bin/agent/hosbah-agent-linux-arm64 ./cmd/agent
	# The same builds under their pre-rename names: a control plane from before the rename serves (and
	# its agent.update fetches) cloudy-agent-linux-*. Drop once no such control plane is left.
	cp bin/agent/hosbah-agent-linux-amd64 bin/agent/cloudy-agent-linux-amd64
	cp bin/agent/hosbah-agent-linux-arm64 bin/agent/cloudy-agent-linux-arm64
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
	go build -o bin/hosbah-api ./cmd/api

test: ## Run tests
	go test ./...

cli: ## The hosbah CLI for Windows, macOS and Linux (amd64 + arm64) into bin/cli
	mkdir -p bin/cli
	for os in linux darwin windows; do for arch in amd64 arm64; do \
		ext=""; [ $$os = windows ] && ext=".exe"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "-s -w -X main.version=$(AGENT_VERSION)" -o bin/cli/hosbah-$$os-$$arch$$ext ./cmd/hosbah || exit 1; \
	done; done
