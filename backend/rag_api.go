package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"
)

// REST for the «Похожие задачи» screen (day 21: indexing + the 2-strategy
// comparison; day 22+ adds querying on top of the same index).

// reindexHandler rebuilds the RAG index from every eligible chat (see
// BuildIndex) and persists it, replacing whatever index existed before.
// Synchronous: the corpus is small enough (a few hundred chunks) that this
// finishes well inside the timeout below, and the caller needs the fresh
// stats back immediately for the comparison table.
func reindexHandler(agent *Agent, ragStore *RagStore, ollama *OllamaClient, embedModel string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Generous: a CPU-only Ollama instance embeds a few hundred chunks in
		// small batches (see embedBatchSize) and that can add up past any
		// "normal" API timeout — this is a manual admin action, not a chat
		// turn, so waiting is fine.
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Minute)
		defer cancel()

		idx, err := BuildIndex(ctx, agent.AllChats(), ollama, embedModel)
		if err != nil {
			if errors.Is(err, ErrOllamaUnavailable) {
				writeError(w, http.StatusBadGateway, "Ollama недоступна: "+err.Error()+" — проверьте, что Ollama запущена и OLLAMA_EMBED_MODEL скачана (ollama pull)")
				return
			}
			log.Printf("rag: build index failed: %v", err)
			writeError(w, http.StatusInternalServerError, "не удалось построить индекс")
			return
		}

		if err := ragStore.Save(idx); err != nil {
			log.Printf("rag: save index failed: %v", err)
			writeError(w, http.StatusInternalServerError, "не удалось сохранить индекс")
			return
		}

		writeJSON(w, http.StatusOK, indexStatusFromIndex(idx))
	}
}

// getIndexHandler reports the current index's state without rebuilding it —
// Exists=false (not an error) when no index has ever been built.
func getIndexHandler(ragStore *RagStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idx, err := ragStore.Load()
		if err != nil {
			log.Printf("rag: load index failed: %v", err)
			writeError(w, http.StatusInternalServerError, "не удалось прочитать индекс")
			return
		}
		if idx == nil {
			writeJSON(w, http.StatusOK, IndexStatus{Exists: false})
			return
		}
		writeJSON(w, http.StatusOK, indexStatusFromIndex(idx))
	}
}

// chunksHandler returns a slice of actual chunk content for one strategy —
// embeddings are 768 numbers, useless to read, so this is the inspection
// path for "what did chunking actually produce" instead (the UI only shows
// aggregate counts; a person who wants to read real chunks uses this
// endpoint directly, e.g. via curl).
func chunksHandler(ragStore *RagStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idx, err := ragStore.Load()
		if err != nil {
			log.Printf("rag: load index failed: %v", err)
			writeError(w, http.StatusInternalServerError, "не удалось прочитать индекс")
			return
		}
		if idx == nil {
			writeError(w, http.StatusNotFound, "индекс ещё не построен")
			return
		}

		strategy := ChunkStrategy(r.URL.Query().Get("strategy"))
		if strategy != ChunkStrategyFixed && strategy != ChunkStrategyStructural {
			writeError(w, http.StatusBadRequest, "параметр strategy должен быть fixed_size или structural")
			return
		}

		limit := 5
		if v := r.URL.Query().Get("limit"); v != "" {
			if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 100 {
				limit = parsed
			}
		}

		previews := make([]ChunkPreview, 0, limit)
		for _, c := range idx.Chunks {
			if c.Strategy != strategy {
				continue
			}
			previews = append(previews, ChunkPreview{
				ID: c.ID, SessionID: c.SessionID, Title: c.Title, Section: c.Section, Strategy: c.Strategy, Text: c.Text,
			})
			if len(previews) == limit {
				break
			}
		}
		writeJSON(w, http.StatusOK, previews)
	}
}

func indexStatusFromIndex(idx *RagIndex) IndexStatus {
	builtAt := idx.BuiltAt
	return IndexStatus{
		Exists:      true,
		BuiltAt:     &builtAt,
		SourceCount: idx.SourceCount,
		EmbedModel:  idx.EmbedModel,
		Strategies:  ComputeStats(idx),
	}
}
