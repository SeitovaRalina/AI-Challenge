package main

import (
	"context"
	"errors"
	"log"
	"net/http"
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
		ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
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
