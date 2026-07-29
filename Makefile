# Makefile for the LLM Gateway. Run `make help` to list targets.
# (More targets — compose-up, k8s-deploy, loadtest — arrive in later phases.)

# Load .env if present so `make run` picks up local config.
ifneq (,$(wildcard .env))
include .env
export
endif

BINARY := bin/gateway

.PHONY: help run build test vet fmt tidy ci docker-build docker-run

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

ci: ## Run the CI checks locally (vet + race tests + build)
	go vet ./...
	go test -race ./...
	CGO_ENABLED=0 go build ./...

docker-build: ## Build the container image (multi-stage, distroless)
	docker build -t llm-gateway:latest .

docker-run: ## Run the container locally (reads .env)
	docker run --rm -p 8080:8080 --env-file .env llm-gateway:latest
