// Package providers defines the Provider abstraction over upstream LLM APIs
// (OpenAI, Anthropic, ...) and the concrete implementations of it.
//
// Everything the rest of the gateway needs from an LLM lives behind the
// Provider interface. In Go an interface is just a set of method signatures;
// any type that has those methods satisfies the interface automatically —
// there is no "implements" keyword like in Java/TypeScript. That lets us swap
// a real OpenAI client for a mock in tests without changing handler code.
package providers

import "context"

// Role identifies who authored a chat message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a single chat message in a conversation.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the provider-agnostic input for a chat completion.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// StreamChunk is one piece of a streaming completion. For any given chunk
// exactly one of these is meaningful:
//   - Content != "" : a token delta to forward to the client.
//   - Err != nil     : the stream failed mid-flight; this is the last chunk.
//   - Done == true   : the provider signalled a normal end-of-stream.
type StreamChunk struct {
	Content string
	Done    bool
	Err     error
}

// Provider is the interface every upstream LLM integration implements.
//
// StreamChat starts a streaming chat completion and returns a receive-only
// channel (<-chan) of chunks. The provider runs the network read loop in its
// own goroutine and closes the channel when the stream ends. Returning a
// channel (rather than, say, a callback) is idiomatic Go: the caller just
// `range`s over it, and cancelling ctx stops the producer.
//
// The returned error is for failures that happen *before* streaming begins
// (bad request, connection refused, non-200 status). Failures *during*
// streaming arrive on the channel as a StreamChunk with Err set.
type Provider interface {
	// Name returns a short identifier used in logs and metrics (e.g. "openai").
	Name() string

	// StreamChat begins streaming a completion for req.
	StreamChat(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
}
