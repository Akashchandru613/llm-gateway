package server

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	// requestIDHeader is read from the inbound request (if a proxy/client set
	// one) and always echoed on the response, so a single request can be traced
	// across the client, the gateway, and downstream logs.
	requestIDHeader = "X-Request-ID"
	// loggerCtxKey stores a per-request *slog.Logger (pre-tagged with the
	// request id) on the gin.Context so handlers log with correlation for free.
	loggerCtxKey = "logger"
)

// requestLogger is Gin middleware that emits one structured JSON log line per
// request (method, path, status, latency, size, client IP, request id) and
// stores a request-scoped logger on the context. It is registered outermost so
// it still logs requests that panic (Recovery turns those into a 500).
func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		rid := c.GetHeader(requestIDHeader)
		if rid == "" {
			rid = newRequestID()
		}
		c.Header(requestIDHeader, rid)
		c.Set(loggerCtxKey, s.log.With("request_id", rid))

		c.Next()

		status := c.Writer.Status()
		attrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes", c.Writer.Size(),
			"client_ip", c.ClientIP(),
		}
		l := s.logger(c)
		switch {
		case status >= 500:
			l.Error("request completed", attrs...)
		case status >= 400:
			l.Warn("request completed", attrs...)
		default:
			l.Info("request completed", attrs...)
		}
	}
}

// logger returns the request-scoped logger (carrying the request id) if the
// middleware has attached one, otherwise the base logger. Handlers use this so
// their own log lines are correlated with the access log.
func (s *Server) logger(c *gin.Context) *slog.Logger {
	if v, ok := c.Get(loggerCtxKey); ok {
		if l, ok := v.(*slog.Logger); ok {
			return l
		}
	}
	return s.log
}

// newRequestID returns a short random hex id. It falls back to a timestamp only
// if the OS entropy source fails, so a request is never left without an id.
func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}
