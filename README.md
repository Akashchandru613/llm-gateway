# LLM Gateway

A high-concurrency reverse proxy that sits between client applications and LLM
providers (OpenAI, Anthropic). Apps call the gateway instead of calling
providers directly, so cross-cutting concerns — **auth, per-key rate limiting,
response caching, provider routing with automatic fallback, token streaming,
and full observability** — live in one central place.

This is the same infrastructure pattern behind Cloudflare AI Gateway, Kong AI
Gateway, LiteLLM, and Portkey.

> **Status:** Phase 1 complete — core SSE streaming proxy to OpenAI behind a
> `Provider` interface, with health probes and handler tests. See the
> [roadmap](#roadmap) below.

## Architecture

```
client app ──POST /v1/chat──▶ ┌─────────────────────────────────────────────┐ ──▶ LLM provider
                              │ Gateway                                       │     (OpenAI / Anthropic)
                              │  auth → rate-limit → cache → route+fallback   │
                              │       → stream tokens back (SSE)              │
                              └───────────────┬───────────────┬──────────────┘
                                              │               │
                                        Redis ┘   Prometheus ─┴─▶ Grafana
                                  (token bucket +    (/metrics)
                                   response cache)
```

The gateway runs a middleware pipeline on every request. Phase 1 implements the
request parsing, provider call, and SSE streaming legs; Redis, metrics, and the
rest land in later phases.

## Tech stack

| Concern              | Choice                                          |
| -------------------- | ----------------------------------------------- |
| Language             | Go 1.22+                                         |
| HTTP framework       | Gin                                             |
| Provider I/O         | **Raw `net/http`** behind a `Provider` interface |
| Streaming to client  | Server-Sent Events (SSE)                        |
| Cache + rate limit   | Redis (Phase 2)                                 |
| Metrics / dashboards | Prometheus + Grafana (Phase 3)                  |
| Logging              | `log/slog` (structured JSON)                    |
| Containerization     | Docker, multi-stage (Phase 3)                   |
| Orchestration        | Kubernetes via `kind` (Phase 4)                 |
| Load testing         | k6 (Phase 4)                                    |
| Config               | 12-factor: environment variables                |
| Tests                | stdlib `testing`, table-driven; `httptest`      |

## Project layout

```
llm-gateway/
  cmd/gateway/main.go        # entrypoint: config, wiring, start + graceful shutdown
  internal/
    server/                  # Gin engine, routes, handlers (+ tests)
    middleware/              # auth, request-id, logging (Phase 2–3)
    providers/               # Provider interface; openai.go, mock.go
    cache/                   # Redis response cache (Phase 2)
    ratelimit/               # Redis token-bucket limiter (Phase 2)
    metrics/                 # Prometheus collectors (Phase 3)
    config/                  # env-based config loader
  deploy/                    # Dockerfile, compose, k8s, grafana (Phase 3–4)
  loadtest/                  # k6 scripts (Phase 4)
  .env.example
  Makefile
```

## Quick start (Phase 1)

Prerequisites: Go 1.22+ (`go version`).

```bash
# 1. Configure
cp .env.example .env
# edit .env and set OPENAI_API_KEY=sk-...

# 2. Run (Makefile loads .env automatically)
make run
# → {"time":...,"level":"INFO","msg":"gateway listening","addr":":8080"}
```

In another terminal:

```bash
# Health probes
curl -s localhost:8080/healthz   # {"status":"ok"}
curl -s localhost:8080/readyz    # {"status":"ready"}

# Streamed chat completion (default). Watch tokens arrive one SSE event at a time:
curl -N -X POST localhost:8080/v1/chat \
  -H 'Content-Type: application/json' \
  -d '{
        "model": "gpt-4o-mini",
        "messages": [{"role": "user", "content": "Say hello in 3 words."}]
      }'
# → data: {"content":"Hello"}
#   data: {"content":" there"}
#   ...
#   data: [DONE]

# Non-streamed (buffered) response:
curl -s -X POST localhost:8080/v1/chat \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini","stream":false,
       "messages":[{"role":"user","content":"Say hi."}]}'
```

`-N` disables curl's output buffering so you actually see the stream tick.

## Configuration

| Variable          | Default                     | Description                             |
| ----------------- | --------------------------- | --------------------------------------- |
| `PORT`            | `8080`                      | Port the gateway listens on             |
| `OPENAI_API_KEY`  | _(required)_                | OpenAI API key                          |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` | Override to target a mock/proxy         |
| `REQUEST_TIMEOUT` | `60s`                       | Upstream per-request timeout (duration) |

## API

### `POST /v1/chat`

Request body:

```json
{
  "model": "gpt-4o-mini",
  "messages": [{ "role": "user", "content": "Hello" }],
  "stream": true
}
```

- `stream` defaults to `true`. When true, the response is `text/event-stream`
  with `data: {"content":"..."}` events terminated by `data: [DONE]`.
- When `false`, the response is a single JSON object.

### `GET /healthz` · `GET /readyz`

Liveness and readiness probes for Kubernetes.

## Development

```bash
make test    # go test ./...   (table-driven handler tests, mock provider)
make vet     # go vet ./...
make fmt     # gofmt -w .
make build   # → bin/gateway
make help    # list all targets
```

Tests never hit OpenAI: handler tests use an in-memory `MockProvider` that
satisfies the same `Provider` interface as the real client.

## Roadmap

- [x] **Phase 1** — Core SSE streaming proxy (OpenAI), health probes, tests.
- [ ] **Phase 2** — Redis response cache + token-bucket rate limiting + API-key auth.
- [ ] **Phase 3** — Prometheus metrics, structured request logging, Docker, docker-compose, Grafana dashboard.
- [ ] **Phase 4** — Kubernetes manifests (kind), provider fallback, HPA, k6 load test.
- [ ] **Phase 5** — Stretch: Helm chart, gRPC API, Terraform, rule-based routing, per-key budgets.
