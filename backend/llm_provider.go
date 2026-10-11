package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type LLMProvider string

const (
	ProviderOllama  LLMProvider = "ollama"
	ProviderLiteLLM LLMProvider = "litellm"
)

var ErrProviderUnavailable = errors.New("selected LLM provider is not configured")
var ErrInvalidProvider = errors.New("invalid LLM provider")

func localOllamaURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("OLLAMA_CHAT_URL must be a local HTTP origin")
	}
	host := parsed.Hostname()
	if host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
		return "", errors.New("OLLAMA_CHAT_URL must point to this computer")
	}
	return strings.TrimRight(raw, "/"), nil
}

func (p LLMProvider) Valid() bool {
	return p == ProviderOllama || p == ProviderLiteLLM
}

type providerContextKey struct{}

func withProvider(ctx context.Context, client *LiteLLMClient) context.Context {
	return context.WithValue(ctx, providerContextKey{}, client)
}

// clientFor uses the client captured at the start of a chat turn. Callers
// outside chat turns retain the original cloud behavior.
func (a *Agent) clientFor(ctx context.Context) *LiteLLMClient {
	if client, ok := ctx.Value(providerContextKey{}).(*LiteLLMClient); ok {
		return client
	}
	return a.client
}

func (a *Agent) providerClient(provider LLMProvider) (*LiteLLMClient, error) {
	switch provider {
	case ProviderOllama:
		if a.localClient != nil {
			return a.localClient, nil
		}
	case ProviderLiteLLM, "":
		if a.client != nil {
			return a.client, nil
		}
	default:
		return nil, ErrInvalidProvider
	}
	return nil, ErrProviderUnavailable
}

func cloudOnly(client *LiteLLMClient, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if client == nil {
			writeAgentError(w, ErrProviderUnavailable)
			return
		}
		next(w, r)
	}
}

type providerStatus struct {
	OllamaAvailable bool   `json:"ollama_available"`
	OllamaModel     string `json:"ollama_model"`
	CloudConfigured bool   `json:"cloud_configured"`
}

func providerStatusHandler(cloudConfigured bool, ollamaURL, model string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := providerStatus{OllamaModel: model, CloudConfigured: cloudConfigured}
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get(strings.TrimRight(ollamaURL, "/") + "/api/tags")
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var tags struct {
					Models []struct {
						Name string `json:"name"`
					} `json:"models"`
				}
				if json.NewDecoder(resp.Body).Decode(&tags) == nil {
					for _, installed := range tags.Models {
						if installed.Name == model {
							status.OllamaAvailable = true
							break
						}
					}
				}
			}
		}
		writeJSON(w, http.StatusOK, status)
	}
}
