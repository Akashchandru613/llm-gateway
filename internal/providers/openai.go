package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIProvider talks to OpenAI's Chat Completions API over raw HTTP.
//
// We deliberately use net/http instead of an SDK: a gateway's whole job is to
// control the wire (streaming, timeouts, cancellation, fallback), which is
// exactly what SDKs hide. Raw HTTP also keeps this struct free of third-party
// types and trivial to reason about.
type OpenAIProvider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewOpenAIProvider constructs an OpenAIProvider.
//
// The http.Client timeout bounds the entire request *including* the streamed
// body, so we keep it generous; finer-grained control comes from the context
// passed to StreamChat. baseURL lets tests point at an httptest server.
func NewOpenAIProvider(apiKey, baseURL string, timeout time.Duration) *OpenAIProvider {
	return &OpenAIProvider{
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: NewHTTPClient(timeout),
	}
}

// Name implements Provider.
func (p *OpenAIProvider) Name() string { return "openai" }

// openAIRequest is the wire format we send to OpenAI. It is unexported so the
// package's public API stays provider-agnostic.
type openAIRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

// openAIStreamChunk is the subset of OpenAI's SSE payload we care about.
type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// StreamChat implements Provider by POSTing to /chat/completions with
// "stream": true and parsing the Server-Sent Events response.
func (p *OpenAIProvider) StreamChat(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	body, err := json.Marshal(openAIRequest{
		Model:    req.Model,
		Messages: req.Messages,
		Stream:   true,
	})
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	// http.NewRequestWithContext binds the request to ctx: if ctx is cancelled
	// (client disconnects, timeout fires) the in-flight HTTP call is aborted.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: do request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// Surface the upstream error to the caller (who turns it into a 5xx).
		// We must close the body ourselves on this early-return path.
		defer resp.Body.Close()
		return nil, fmt.Errorf("openai: unexpected status %d: %s", resp.StatusCode, readErrorBody(resp.Body))
	}

	out := make(chan StreamChunk)

	// Producer goroutine: reads the SSE body and forwards deltas onto `out`.
	// `go func(){ ... }()` launches a lightweight green thread (cheaper than an
	// OS thread; think of it like an async task in JS). The two defers run when
	// the goroutine returns, guaranteeing we close the body and the channel
	// exactly once so the consumer's `range` terminates.
	go func() {
		defer resp.Body.Close()
		defer close(out)

		scanner := bufio.NewScanner(resp.Body)
		// Individual SSE lines can exceed bufio's default 64KB cap; raise it.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			// OpenAI streams SSE lines of the form "data: {json}" separated by
			// blank lines. Ignore anything that is not a data line.
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				send(ctx, out, StreamChunk{Done: true})
				return
			}

			var chunk openAIStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				send(ctx, out, StreamChunk{Err: fmt.Errorf("openai: decode chunk: %w", err)})
				return
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			if content := chunk.Choices[0].Delta.Content; content != "" {
				if !send(ctx, out, StreamChunk{Content: content}) {
					return // ctx cancelled; stop producing
				}
			}
		}

		if err := scanner.Err(); err != nil {
			send(ctx, out, StreamChunk{Err: fmt.Errorf("openai: read stream: %w", err)})
		}
	}()

	return out, nil
}

// send delivers chunk on out unless ctx is cancelled first. It returns false if
// ctx won the race, signalling the producer to stop. `select` is Go's way to
// wait on several channel operations at once and proceed with whichever is
// ready — here, "send the chunk" vs. "give up because we were cancelled".
func send(ctx context.Context, out chan<- StreamChunk, chunk StreamChunk) bool {
	select {
	case out <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}

// readErrorBody reads a bounded prefix of an error response for logging,
// without risking an unbounded read of a hostile/huge body.
func readErrorBody(r io.Reader) string {
	const max = 2 << 10 // 2 KB
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return strings.TrimSpace(string(b))
}
