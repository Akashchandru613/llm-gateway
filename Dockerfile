# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.25 AS build
WORKDIR /src

# Download modules in a separate layer so they're cached across source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO_ENABLED=0 produces a fully static binary (no libc dependency) so it runs
# on a minimal distroless base. -ldflags "-s -w" strips debug info to shrink it.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/gateway ./cmd/gateway

# ---- runtime stage ----
# distroless/static has no shell or package manager (small attack surface) but
# does ship CA certificates, which we need for TLS to the OpenAI API.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gateway /gateway
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/gateway"]
