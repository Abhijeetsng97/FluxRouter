// Package upstream mirrors old/src/upstream.ts: forward OpenAI-compatible
// chat completion requests. Streaming passes chunks through chunk-by-chunk
// (never buffered whole). Connection pooling mirrors undici's Agent options
// (32 connections, 30s keep-alive idle timeout).
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/abhijeet/fluxrouter/internal/types"
)

// Credentials mirrors upstream.ts Upstreams.
type Credentials struct {
	Ollama     Endpoint
	OpenRouter Endpoint
}

// Endpoint mirrors one { baseUrl, apiKey } pair.
type Endpoint struct {
	BaseURL string
	APIKey  string
}

// ForwardOptions mirrors upstream.ts ForwardOptions.
type ForwardOptions struct {
	Body   any
	Stream bool
}

// Result mirrors upstream.ts ForwardResult.
type Result struct {
	Status    int
	Headers   map[string]string
	Body      io.ReadCloser // nil => buffered json
	JSON      any           // non-stream convenience
	LatencyMs int64
}

// Client holds pooled transports per upstream (undici Agent equivalent).
type Client struct {
	mu    sync.Mutex
	trans map[string]*http.Client
}

// NewClient creates the pooled client set.
func NewClient() *Client {
	return &Client{trans: map[string]*http.Client{}}
}

// agentFor mirrors upstream.ts agentFor: pooled keep-alive transport.
func (c *Client) transportFor(upstream string) *http.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.trans[upstream]; ok {
		return t
	}
	t := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        32,
			MaxIdleConnsPerHost: 32,
			IdleConnTimeout:     30 * time.Second,
			// Match undici Agent behavior: no automatic compression, we
			// forward identity bodies and strip hop headers ourselves.
			DisableCompression: true,
		},
	}
	c.trans[upstream] = t
	return t
}

// CloseAll mirrors upstream.ts closeAllAgents.
func (c *Client) CloseAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.trans {
		delete(c.trans, k)
	}
}

func endpointFor(baseURL, path string) string {
	return strings.TrimSuffix(baseURL, "/") + path
}

func headersFor(upstream string, apiKey string) map[string]string {
	h := map[string]string{"Content-Type": "application/json"}
	if apiKey != "" {
		h["Authorization"] = "Bearer " + apiKey
	}
	if upstream == "openrouter" {
		h["HTTP-Referer"] = "https://github.com/fluxrouter"
		h["X-Title"] = "FluxRouter"
	}
	return h
}

// Forward mirrors upstream.ts forward.
func (c *Client) Forward(ctx context.Context, upstream Endpoint, upstreamName, path string, opts ForwardOptions) (*Result, error) {
	started := time.Now()

	payload, err := json.Marshal(opts.Body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointFor(upstream.BaseURL, path), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	for k, v := range headersFor(upstreamName, upstream.APIKey) {
		req.Header.Set(k, v)
	}
	if opts.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}

	res, err := c.transportFor(upstreamName).Do(req)
	if err != nil {
		return nil, err
	}
	latency := time.Since(started).Milliseconds()

	hdrs := map[string]string{}
	for k := range res.Header {
		hdrs[strings.ToLower(k)] = res.Header.Get(k)
	}

	if opts.Stream {
		if res.Body == nil {
			return nil, fmt.Errorf("upstream %s returned no body for stream request", upstreamName)
		}
		return &Result{Status: res.StatusCode, Headers: hdrs, Body: res.Body, JSON: nil, LatencyMs: latency}, nil
	}
	defer res.Body.Close()
	text, err := io.ReadAll(res.Body)
	if err != nil && !errorsIsEOF(err) {
		return &Result{Status: res.StatusCode, Headers: hdrs, JSON: map[string]any{"raw": string(text)}, LatencyMs: latency}, nil
	}
	var jsonBody any
	if len(text) > 0 {
		if err := json.Unmarshal(text, &jsonBody); err != nil {
			jsonBody = map[string]any{"raw": string(text)}
		}
	}
	return &Result{Status: res.StatusCode, Headers: hdrs, JSON: jsonBody, LatencyMs: latency}, nil
}

func errorsIsEOF(err error) bool {
	return err != nil && (err == io.EOF || strings.Contains(err.Error(), "unexpected EOF"))
}

// PickModel mirrors upstream.ts pickModel.
func PickModel(tierModels []types.TierModel, preferUpstream string) *types.TierModel {
	if preferUpstream != "" {
		for i := range tierModels {
			if string(tierModels[i].Upstream) == preferUpstream {
				return &tierModels[i]
			}
		}
	}
	if len(tierModels) == 0 {
		return nil
	}
	return &tierModels[0]
}

// UpstreamModelID mirrors upstream.ts upstreamModelId.
func UpstreamModelID(m *types.TierModel) string { return m.ID }