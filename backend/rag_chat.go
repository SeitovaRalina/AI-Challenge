package main

import (
	"context"
	"log"
	"strings"
)

// Day 25: the main "Ассистент по оценке задач" chat grounds itself in the
// same RAG index the standalone «Похожие задачи» screen (days 21-24)
// builds — in-process, not a model-invoked MCP tool. Every other tool this
// agent can call (agent_tools.go) goes through a real MCP server
// subprocess, which is both heavier than this needs and a poor fit: that
// routing step explicitly skips tools for "describing a task, asking to
// estimate one, refining an estimate" — exactly when grounding in similar
// past sessions is most useful. So retrieval here runs unconditionally for
// every real turn of a RAG-enabled, non-lab chat, the same way task/project
// memory already does (buildMemorySystemMessages), not at the model's
// discretion.

// ragChatStrategy/defaultTopK ground a main-chat turn — reuses the
// standalone screen's structural strategy (days 21-23 found it retrieves
// better than fixed-size) and the same topK. Cosine-only, no rerank: unlike
// the "Похожие задачи" screen's opt-in rerank/rewrite, every chat turn
// already pays for routing + answering + several concurrent memory
// side-calls (PostMessage), so grounding here stays to the cheap
// embedding-only pass rather than adding another LLM round-trip to every
// single turn.
const ragChatStrategy = ChunkStrategyStructural

// retrieveForChat embeds question and returns the chunks that cleared day
// 24's minConfidenceScore floor, best first — nil (not an error) when idx
// is nil, question is blank, or nothing cleared the floor, so the caller
// treats "no grounding this turn" as the ordinary case it is, not a
// failure. An embedding failure is logged and treated the same way: a chat
// turn must never fail just because Ollama is briefly unreachable.
func retrieveForChat(ctx context.Context, ollama *OllamaClient, idx *RagIndex, question string) []scoredChunk {
	if idx == nil || strings.TrimSpace(question) == "" {
		return nil
	}
	vectors, err := ollama.Embeddings(ctx, []string{question})
	if err != nil {
		log.Printf("agent: rag grounding: embedding failed, answering without it: %v", err)
		return nil
	}
	candidates := retrieveTopK(idx.Chunks, ragChatStrategy, vectors[0], defaultTopK)
	return filterByThreshold(candidates, minConfidenceScore)
}

// ragChatSystemPrompt renders found the same shape buildRagPrompt
// (rag_query.go) uses for the standalone endpoint, plus the chat-specific
// instruction: use it only if it actually answers the live question, and
// the envelope's own "citations" field (agentSystemPrompt) is how to cite
// it — there's no separate "answer" field to fill in here, unlike the
// standalone endpoint.
func ragChatSystemPrompt(found []scoredChunk) string {
	return "Найденный контекст из истории прошлых задач, релевантный текущему вопросу пользователя:\n\n" +
		formatChunksForPrompt(found) +
		"Используй его в ответе ТОЛЬКО если он реально относится к текущему вопросу, и в этом случае обязательно процитируй в поле \"citations\" своего JSON-ответа дословный фрагмент, на который опираешься (см. системный промпт выше). Если ничего из этого не относится к вопросу — не упоминай это и оставь \"citations\" пустым массивом."
}

// AddRagSource wires the RAG index/embedder into chat turns — called once
// from main.go after ragStore/ollamaClient are constructed (they read
// their own env vars, after NewAgent is already called), mirroring
// AddToolSource's own after-the-fact wiring for the same reason.
func (a *Agent) AddRagSource(store *RagStore, ollama *OllamaClient) {
	a.ragStore = store
	a.ragOllama = ollama
}

// SetRagEnabled turns this chat's retrieval grounding on or off — the
// manual, explicit counterpart to newChatLocked's default, mirroring
// SetContextStrategy (context_strategy.go). Lab chats can't be changed,
// same reasoning as SetContextStrategy's own guard: day-10's strategy
// comparison must stay free of anything beyond the ContextStrategy itself.
func (a *Agent) SetRagEnabled(chatID string, enabled bool) (*Chat, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	chat, ok := a.chats[chatID]
	if !ok {
		return nil, ErrChatNotFound
	}
	if chat.LabID != "" {
		return nil, ErrWrongStrategy
	}
	chat.RagEnabled = enabled
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s after rag toggle: %v", chat.ID, err)
	}
	return copyChat(chat), nil
}
