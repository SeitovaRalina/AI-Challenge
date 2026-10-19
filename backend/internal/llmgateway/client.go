package llmgateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

var (
	ErrAuth            = errors.New("litellm: authentication failed")
	ErrUnavailable     = errors.New("litellm: service unavailable")
	ErrContextOverflow = errors.New("litellm: context length exceeded")
)

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, httpClient: &http.Client{Timeout: 220 * time.Second}}
}

// Complete owns the gateway request, classification, and one retry for transient errors.
func (c *Client) Complete(ctx context.Context, reqBody []byte) ([]byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		body, retry, err := c.completeAttempt(ctx, reqBody)
		if !retry || attempt > 0 {
			return body, err
		}
		log.Printf("litellm: temporary gateway error, retrying completion once: %v", err)
		timer := time.NewTimer(750 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("%w: retry loop exhausted", ErrUnavailable)
}

func (c *Client) completeAttempt(ctx context.Context, reqBody []byte) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("%w: reading response: %v", ErrUnavailable, err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, false, fmt.Errorf("%w: status %d", ErrAuth, resp.StatusCode)
	case resp.StatusCode != http.StatusOK && isContextOverflow(string(body)):
		return nil, false, fmt.Errorf("%w: status %d: %s", ErrContextOverflow, resp.StatusCode, truncate(string(body), 300))
	case resp.StatusCode != http.StatusOK:
		retry := resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout
		return nil, retry, fmt.Errorf("%w: status %d: %s", ErrUnavailable, resp.StatusCode, truncate(string(body), 300))
	}
	return body, false, nil
}

func isContextOverflow(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "context_length_exceeded") || strings.Contains(lower, "maximum context length") || strings.Contains(lower, "context window")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
