package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Akashchandru613/llm-gateway/internal/providers"
)

// TestRequestIDHeader verifies the logging middleware attaches an X-Request-ID
// to every response, generating one when absent and echoing a client-supplied
// one so requests can be traced end to end.
func TestRequestIDHeader(t *testing.T) {
	tests := []struct {
		name       string
		incoming   string
		wantEchoed bool
	}{
		{name: "generates an id when absent", incoming: "", wantEchoed: false},
		{name: "echoes a client-supplied id", incoming: "abc-123", wantEchoed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, &providers.MockProvider{})
			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			if tt.incoming != "" {
				req.Header.Set(requestIDHeader, tt.incoming)
			}
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)

			got := rec.Header().Get(requestIDHeader)
			if got == "" {
				t.Fatal("missing X-Request-ID response header")
			}
			if tt.wantEchoed && got != tt.incoming {
				t.Errorf("X-Request-ID = %q, want echoed %q", got, tt.incoming)
			}
		})
	}
}

// TestReadinessProbe verifies /readyz flips to 503 once the server is marked
// not-ready (as happens during graceful shutdown), while /healthz stays up.
func TestReadinessProbe(t *testing.T) {
	s := newTestServer(t, &providers.MockProvider{})

	if code := getStatus(t, s, "/readyz"); code != http.StatusOK {
		t.Fatalf("/readyz (ready) = %d, want 200", code)
	}

	s.SetReady(false)
	if code := getStatus(t, s, "/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz (draining) = %d, want 503", code)
	}

	// Liveness is independent of readiness — the process is still alive.
	if code := getStatus(t, s, "/healthz"); code != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200", code)
	}
}

func getStatus(t *testing.T, s *Server, path string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec.Code
}
