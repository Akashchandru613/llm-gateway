package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// drain consumes a stream channel to completion, returning the assembled
// content, whether a Done chunk arrived, and any mid-stream error.
func drain(ch <-chan StreamChunk) (content string, done bool, streamErr error) {
	var sb strings.Builder
	for c := range ch {
		switch {
		case c.Err != nil:
			streamErr = c.Err
		case c.Done:
			done = true
		default:
			sb.WriteString(c.Content)
		}
	}
	return sb.String(), done, streamErr
}

// TestOpenAIProvider_RequestShape verifies we send the right method, auth
// header, content type, and a stream=true body. Assertions run inside the
// handler goroutine; testing.T's methods are safe for concurrent use.
func TestOpenAIProvider_RequestShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("path = %q, want .../chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
		}
		var body openAIRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body.Model != "gpt-4o-mini" || !body.Stream {
			t.Errorf("body = %+v, want model=gpt-4o-mini stream=true", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewOpenAIProvider("test-key", srv.URL, 5*time.Second)
	ch, err := p.StreamChat(context.Background(), ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamChat returned error: %v", err)
	}
	if _, done, streamErr := drain(ch); !done || streamErr != nil {
		t.Errorf("drain: done=%v err=%v, want done=true err=nil", done, streamErr)
	}
}

// TestOpenAIProvider_ParsesStream feeds the provider OpenAI-format SSE bodies
// and checks how it assembles deltas and handles errors.
func TestOpenAIProvider_ParsesStream(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		wantContent   string
		wantDone      bool
		wantStartErr  bool
		wantStreamErr bool
	}{
		{
			name:   "assembles deltas and ends on [DONE]",
			status: http.StatusOK,
			body: `data: {"choices":[{"delta":{"role":"assistant"}}]}` + "\n\n" +
				`data: {"choices":[{"delta":{"content":"Hello"}}]}` + "\n\n" +
				`data: {"choices":[{"delta":{"content":", world"}}]}` + "\n\n" +
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
				"data: [DONE]\n\n",
			wantContent: "Hello, world",
			wantDone:    true,
		},
		{
			name:         "non-200 status returns a start error",
			status:       http.StatusUnauthorized,
			body:         `{"error":{"message":"invalid api key"}}`,
			wantStartErr: true,
		},
		{
			name:          "malformed chunk yields a stream error",
			status:        http.StatusOK,
			body:          "data: {not valid json}\n\n",
			wantStreamErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if tt.status != http.StatusOK {
					w.WriteHeader(tt.status)
				}
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()

			p := NewOpenAIProvider("k", srv.URL, 5*time.Second)
			ch, err := p.StreamChat(context.Background(), ChatRequest{
				Model:    "m",
				Messages: []Message{{Role: RoleUser, Content: "hi"}},
			})

			if tt.wantStartErr {
				if err == nil {
					t.Fatal("expected a start error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected start error: %v", err)
			}

			content, done, streamErr := drain(ch)
			if tt.wantStreamErr {
				if streamErr == nil {
					t.Error("expected a stream error chunk, got none")
				}
				return
			}
			if streamErr != nil {
				t.Fatalf("unexpected stream error: %v", streamErr)
			}
			if content != tt.wantContent {
				t.Errorf("content = %q, want %q", content, tt.wantContent)
			}
			if done != tt.wantDone {
				t.Errorf("done = %v, want %v", done, tt.wantDone)
			}
		})
	}
}
