package providers

import "context"

// MockProvider is an in-memory Provider for tests and local development. It
// emits a predetermined sequence of tokens, and can optionally fail at start
// (StartErr) or inject a mid-stream error after the tokens (StreamErr).
//
// It lives in this package (not a _test.go file) on purpose, so other packages
// — notably the server tests — can reuse it through the Provider interface.
type MockProvider struct {
	NameValue string   // returned by Name(); defaults to "mock"
	Tokens    []string // content deltas to emit, in order
	StartErr  error    // if non-nil, StreamChat returns this immediately
	StreamErr error    // if non-nil, emitted as a mid-stream chunk after Tokens
}

// Name implements Provider.
func (m *MockProvider) Name() string {
	if m.NameValue != "" {
		return m.NameValue
	}
	return "mock"
}

// StreamChat implements Provider, mirroring the real producer-goroutine shape
// so tests exercise the same concurrency pattern as production code.
func (m *MockProvider) StreamChat(ctx context.Context, _ ChatRequest) (<-chan StreamChunk, error) {
	if m.StartErr != nil {
		return nil, m.StartErr
	}

	out := make(chan StreamChunk)
	go func() {
		defer close(out)
		for _, tok := range m.Tokens {
			if !send(ctx, out, StreamChunk{Content: tok}) {
				return
			}
		}
		if m.StreamErr != nil {
			send(ctx, out, StreamChunk{Err: m.StreamErr})
			return
		}
		send(ctx, out, StreamChunk{Done: true})
	}()
	return out, nil
}
