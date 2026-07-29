package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Akashchandru613/llm-gateway/internal/providers"
)

// chatRequest is the JSON body clients POST to /v1/chat.
type chatRequest struct {
	Model    string              `json:"model"`
	Messages []providers.Message `json:"messages"`
	// Stream is a *bool so we can tell "field absent" (nil → default to
	// streaming) apart from an explicit "stream": false. Go has no optional
	// types, so a pointer is the idiomatic way to model "unset".
	Stream *bool `json:"stream"`
}

func (r chatRequest) wantsStream() bool {
	return r.Stream == nil || *r.Stream
}

// handleChat is the gateway's primary endpoint. It validates the request, asks
// the provider to stream a completion, and forwards tokens to the client as
// Server-Sent Events (or as one JSON object when "stream": false).
func (s *Server) handleChat(c *gin.Context) {
	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body: " + err.Error()})
		return
	}
	if req.Model == "" || len(req.Messages) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "fields 'model' and 'messages' are required"})
		return
	}

	// Propagate the request context so a client disconnect or timeout cancels
	// the upstream provider call instead of leaking it.
	ctx := c.Request.Context()

	stream, err := s.provider.StreamChat(ctx, providers.ChatRequest{
		Model:    req.Model,
		Messages: req.Messages,
	})
	if err != nil {
		s.logger(c).Error("provider failed to start stream", "provider", s.provider.Name(), "error", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream provider error"})
		return
	}

	if req.wantsStream() {
		s.streamSSE(c, stream)
		return
	}
	s.bufferedJSON(c, stream)
}

// streamSSE forwards provider chunks to the client as Server-Sent Events. Each
// event is `data: {"content":"..."}` and the stream ends with `data: [DONE]`.
// We flush after every write so tokens reach the client immediately instead of
// being buffered into one big response.
func (s *Server) streamSSE(c *gin.Context, stream <-chan providers.StreamChunk) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // ask proxies (e.g. nginx) not to buffer

	// http.Flusher lets us push bytes to the client mid-handler. The type
	// assertion `x.(T)` checks whether c.Writer's dynamic type implements it.
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}

	for chunk := range stream {
		switch {
		case chunk.Err != nil:
			s.logger(c).Error("provider stream error", "provider", s.provider.Name(), "error", chunk.Err)
			// Status 200 + headers were already sent on the first write, so we
			// cannot change the status now. Signal the failure in-band as an SSE
			// event, then stop (no [DONE]).
			writeSSEData(c.Writer, `{"error":"upstream stream error"}`)
			flusher.Flush()
			return
		case chunk.Done:
			writeSSEData(c.Writer, "[DONE]")
			flusher.Flush()
			return
		default:
			// JSON-encode the content so embedded newlines/quotes can't break
			// the SSE framing (a literal newline would end the data field early).
			payload, _ := json.Marshal(map[string]string{"content": chunk.Content})
			writeSSEData(c.Writer, string(payload))
			flusher.Flush()
		}
	}
}

// bufferedJSON consumes the whole stream and returns it as a single JSON
// response. Used when the client sends "stream": false.
func (s *Server) bufferedJSON(c *gin.Context, stream <-chan providers.StreamChunk) {
	var sb strings.Builder
	for chunk := range stream {
		if chunk.Err != nil {
			s.logger(c).Error("provider stream error", "provider", s.provider.Name(), "error", chunk.Err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "upstream stream error"})
			return
		}
		if chunk.Done {
			break
		}
		sb.WriteString(chunk.Content)
	}
	c.JSON(http.StatusOK, gin.H{
		"provider": s.provider.Name(),
		"message":  gin.H{"role": "assistant", "content": sb.String()},
	})
}

// writeSSEData writes one SSE frame: "data: <payload>\n\n".
func writeSSEData(w io.Writer, data string) {
	_, _ = io.WriteString(w, "data: "+data+"\n\n")
}

// handleHealthz is a liveness probe: it reports whether the process is up. It
// must stay cheap and dependency-free — Kubernetes restarts the pod when it
// fails, so it should fail only when the process itself is broken.
func (s *Server) handleHealthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleReadyz is a readiness probe: it reports whether we can serve traffic.
// It returns 503 once the server has been marked not-ready (during graceful
// shutdown) so Kubernetes stops routing new requests here while in-flight ones
// drain. In Phase 2 it will additionally check dependencies (e.g. Redis).
func (s *Server) handleReadyz(c *gin.Context) {
	if !s.ready.Load() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
