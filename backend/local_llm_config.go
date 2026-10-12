package main

import (
	"log"
	"os"
	"strconv"
)

// LocalLLMConfig controls only the Ollama chat path. The context setting is
// the application's budget; the model must be configured with a matching
// num_ctx in Ollama itself (see docs/local-llm-day29.md).
type LocalLLMConfig struct {
	Temperature   float64
	MaxTokens     int
	ContextWindow int
}

func defaultLocalLLMConfig() LocalLLMConfig {
	return LocalLLMConfig{Temperature: 0.1, MaxTokens: 1024, ContextWindow: 4096}
}

func localLLMConfigFromEnv() LocalLLMConfig {
	cfg := defaultLocalLLMConfig()
	if raw := os.Getenv("OLLAMA_CHAT_TEMPERATURE"); raw != "" {
		if value, err := strconv.ParseFloat(raw, 64); err == nil && value >= 0 && value <= 2 {
			cfg.Temperature = value
		} else {
			log.Printf("invalid OLLAMA_CHAT_TEMPERATURE %q, using %.1f", raw, cfg.Temperature)
		}
	}
	if raw := os.Getenv("OLLAMA_CHAT_MAX_TOKENS"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value >= 128 && value <= 8192 {
			cfg.MaxTokens = value
		} else {
			log.Printf("invalid OLLAMA_CHAT_MAX_TOKENS %q, using %d", raw, cfg.MaxTokens)
		}
	}
	if raw := os.Getenv("OLLAMA_CHAT_CONTEXT_WINDOW"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value >= 2048 && value <= 32768 {
			cfg.ContextWindow = value
		} else {
			log.Printf("invalid OLLAMA_CHAT_CONTEXT_WINDOW %q, using %d", raw, cfg.ContextWindow)
		}
	}
	return cfg
}

func (a *Agent) effectiveContextLimit(provider LLMProvider) int {
	limit := a.contextTokenLimit
	if provider == ProviderOllama && a.localConfig.ContextWindow > 0 && (limit <= 0 || a.localConfig.ContextWindow < limit) {
		return a.localConfig.ContextWindow
	}
	return limit
}
