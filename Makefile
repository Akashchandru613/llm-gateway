# Makefile for the LLM Gateway. Run `make help` to list targets.
# (More targets — docker-build, compose-up, k8s-deploy, loadtest — arrive in
# later phases.)

# Load .env if present so `make run` picks up local config.
ifneq (,$(wildcard .env))
include .env
export
endif

BINARY := bin/gateway

.PHONY: help run build test vet fmt tidy

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

run: ## Run the gateway locally (reads .env)
	go run ./cmd/gateway

build: ## Compile the binary into bin/
	CGO_ENABLED=0 go build -o $(BINARY) ./cmd/gateway

test: ## Run all tests
	go test ./...

vet: ## Run go vet (static analysis)
	go vet ./...

fmt: ## Format all Go source
	gofmt -w .

tidy: ## Tidy go.mod / go.sum
	go mod tidy
