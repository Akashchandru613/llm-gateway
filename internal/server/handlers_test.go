package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Akashchandru613/llm-gateway/internal/config"
	"github.com/Akashchandru613/llm-gateway/internal/providers"
)

// newTestServer builds a Server wired to the given (usually mock) provider,
// with logging discarded so test output stays clean.
func newTestServer(t *testing.T, p providers.Provider) *Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(&config.Config{Port: "0"}, p, logger)
}

// collectSSE reads an SSE response body and returns the concatenated "content"
// fields, whether the [DONE] sentinel was seen, and whether an error event
// appeared.
func collectSSE(t *testing.T, body io.Reader) (content string, done, sawError bool) {
	t.Helper()
	scanner := bufio.NewScanner(body)
	var sb strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			done = true
			continue
		}
		var payload map[string]string
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			t.Fatalf("bad SSE payload %q: %v", data, err)
		}
		if e := payload["error"]; e != "" {
			sawError = true
			continue
		}
		sb.WriteString(payload["content"])
	}
	return sb.String(), done, sawError
}

// wantStatus returns a verify func that only asserts the HTTP status code.
func wantStatus(code int) func(*testing.T, *httptest.ResponseRecorder) {
	return func(t *testing.T, rec *httptest.ResponseRecorder) {
		if rec.Code != code {
			t.Fatalf("status = %d, want %d (body: %s)", rec.Code, code, rec.Body.String())
		}
	}
}

func TestHandleChat(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		provider providers.Provider
		verify   func(t *testing.T, rec *httptest.ResponseRecorder)
	}{
		{
			name:     "streaming happy path forwards all tokens then [DONE]",
			body:     `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`,
			provider: &providers.MockProvider{Tokens: []string{"Hel", "lo", "!"}},
			verify: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200", rec.Code)
				}
				if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
					t.Fatalf("Content-Type = %q, want text/event-stream", ct)
				}
				content, done, sawErr := collectSSE(t, rec.Body)
				if content != "Hello!" {
					t.Errorf("assembled content = %q, want %q", content, "Hello!")
				}
				if !done {
					t.Error("missing [DONE] sentinel")
				}
				if sawErr {
					t.Error("unexpected error event")
				}
			},
		},
		{
			name:     "stream=false returns buffered JSON",
			body:     `{"model":"gpt-4o-mini","stream":false,"messages":[{"role":"user","content":"hi"}]}`,
			provider: &providers.MockProvider{Tokens: []string{"a", "b", "c"}},
			verify: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200", rec.Code)
				}
				if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
					t.Fatalf("Content-Type = %q, want application/json", ct)
				}
				var resp struct {
					Provider string `json:"provider"`
					Message  struct {
						Content string `json:"content"`
					} `json:"message"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if resp.Message.Content != "abc" {
					t.Errorf("content = %q, want %q", resp.Message.Content, "abc")
				}
				if resp.Provider != "mock" {
					t.Errorf("provider = %q, want %q", resp.Provider, "mock")
				}
			},
		},
		{
			name:     "invalid JSON returns 400",
			body:     `{not json`,
			provider: &providers.MockProvider{},
			verify:   wantStatus(http.StatusBadRequest),
		},
		{
			name:     "missing model returns 400",
			body:     `{"messages":[{"role":"user","content":"hi"}]}`,
			provider: &providers.MockProvider{},
			verify:   wantStatus(http.StatusBadRequest),
		},
		{
			name:     "empty messages returns 400",
			body:     `{"model":"gpt-4o-mini","messages":[]}`,
			provider: &providers.MockProvider{},
			verify:   wantStatus(http.StatusBadRequest),
		},
		{
			name:     "provider start error returns 502",
			body:     `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`,
			provider: &providers.MockProvider{StartErr: errors.New("boom")},
			verify:   wantStatus(http.StatusBadGateway),
		},
		{
			name:     "mid-stream error surfaces as an SSE error event",
			body:     `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`,
			provider: &providers.MockProvider{Tokens: []string{"partial"}, StreamErr: errors.New("upstream died")},
			verify: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200 (headers sent before the error)", rec.Code)
				}
				content, done, sawErr := collectSSE(t, rec.Body)
				if content != "partial" {
					t.Errorf("content = %q, want %q", content, "partial")
				}
				if !sawErr {
					t.Error("expected an SSE error event")
				}
				if done {
					t.Error("did not expect [DONE] after a mid-stream error")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, tt.provider)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			s.Handler().ServeHTTP(rec, req)

			tt.verify(t, rec)
		})
	}
}

func TestHealthEndpoints(t *testing.T) {
	s := newTestServer(t, &providers.MockProvider{})
	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", path, rec.Code)
		}
	}
}
