// Package server builds the Gin HTTP engine, wires in dependencies, and
// registers the gateway's routes.
package server

import (
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"github.com/Akashchandru613/llm-gateway/internal/config"
	"github.com/Akashchandru613/llm-gateway/internal/providers"
)

// Server holds the router and its dependencies. Carrying dependencies on a
// struct (instead of using package-level globals) is the idiomatic Go way to
// do dependency injection: tests construct a Server with fakes, production
// constructs it with real implementations.
type Server struct {
	cfg      *config.Config
	provider providers.Provider
	log      *slog.Logger
	engine   *gin.Engine

	// ready gates the readiness probe. It starts true and is flipped to false
	// at the start of graceful shutdown so Kubernetes stops routing new traffic
	// to this pod before we drain in-flight requests.
	ready atomic.Bool
}

// New constructs a Server and registers all routes.
func New(cfg *config.Config, provider providers.Provider, log *slog.Logger) *Server {
	s := &Server{
		cfg:      cfg,
		provider: provider,
		log:      log,
	}
	s.ready.Store(true)

	engine := gin.New()
	// gin.New() has NO middleware by default (unlike gin.Default()). We add our
	// own: requestLogger (structured JSON access logs + request ids) outermost
	// so it still logs panicking requests, then Recovery, which converts a panic
	// in a handler into a 500 instead of crashing the whole process.
	engine.Use(s.requestLogger(), gin.Recovery())

	s.registerRoutes(engine)
	s.engine = engine
	return s
}

// SetReady flips the readiness flag reported by GET /readyz. main() sets it to
// false when a shutdown signal arrives so load balancers drain this instance
// before the process exits.
func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}

// Handler exposes the underlying http.Handler so main() can mount it on an
// http.Server and tests can drive it with net/http/httptest.
func (s *Server) Handler() http.Handler {
	return s.engine
}

func (s *Server) registerRoutes(r *gin.Engine) {
	r.GET("/healthz", s.handleHealthz)
	r.GET("/readyz", s.handleReadyz)

	v1 := r.Group("/v1")
	{
		v1.POST("/chat", s.handleChat)
	}
}
