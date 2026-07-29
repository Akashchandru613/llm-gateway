package providers

import (
	"net/http"
	"time"
)

// NewHTTPClient builds an http.Client tuned for a gateway that fans many
// concurrent requests out to a small number of upstream provider hosts.
//
// Go's http.DefaultTransport caps MaxIdleConnsPerHost at 2. Under load that
// forces the gateway to keep reopening (and re-TLS-handshaking) connections to
// a single host like api.openai.com — a real throughput bottleneck. We raise
// the idle-connection pool so keep-alive connections are reused across requests
// instead of thrashed. The timeout bounds the whole upstream call; per-request
// cancellation still flows through the context passed to StreamChat.
func NewHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}
