package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"
)

// REST for the «Похожие задачи» screen (day 21: indexing + the 2-strategy
// chunking comparison; day 22: querying — RAG vs no-RAG on one question,
// and the same comparison run over all 10 control questions).

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

// queryRequest is the payload accepted by POST /api/rag/query.
type queryRequest struct {
	Question string `json:"question"`
	Mode     string `json:"mode"`     // "rag" | "no_rag"
	Strategy string `json:"strategy"` // required when mode is "rag"
}

// queryHandler answers one question in either mode — the single primitive
// the comparison UI and the eval runs below both build on.
func queryHandler(client *LiteLLMClient, ollama *OllamaClient, ragStore *RagStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req queryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}
		question := req.Question
		if question == "" {
			writeError(w, http.StatusBadRequest, "вопрос не может быть пустым")
			return
		}
		if req.Mode != "rag" && req.Mode != "no_rag" {
			writeError(w, http.StatusBadRequest, "mode должен быть rag или no_rag")
			return
		}

		var idx *RagIndex
		strategy := ChunkStrategy(req.Strategy)
		if req.Mode == "rag" {
			if strategy != ChunkStrategyFixed && strategy != ChunkStrategyStructural {
				writeError(w, http.StatusBadRequest, "для mode=rag нужен strategy: fixed_size или structural")
				return
			}
			loaded, err := ragStore.Load()
			if err != nil {
				log.Printf("rag: load index failed: %v", err)
				writeError(w, http.StatusInternalServerError, "не удалось прочитать индекс")
				return
			}
			if loaded == nil {
				writeError(w, http.StatusConflict, "индекс ещё не построен — сначала «Переиндексировать»")
				return
			}
			idx = loaded
		}

		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()

		answer, err := Query(ctx, client, ollama, idx, question, req.Mode, strategy, defaultTopK)
		if err != nil {
			writeRagQueryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, answer)
	}
}

// getEvalQuestionsHandler returns the day-22 control-question set so the UI
// doesn't need its own copy of it.
func getEvalQuestionsHandler(evalPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		questions, err := loadEvalQuestions(evalPath)
		if err != nil {
			log.Printf("rag: load eval questions failed: %v", err)
			writeError(w, http.StatusInternalServerError, "не удалось прочитать контрольные вопросы: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, questions)
	}
}

// evalRunRequest is the payload accepted by POST /api/rag/eval/run.
type evalRunRequest struct {
	Strategy string `json:"strategy"`
}

// evalRunHandler is day 22's actual deliverable: every control question
// answered both without and with RAG (20 LLM calls total), so quality can
// be compared question by question.
func evalRunHandler(client *LiteLLMClient, ollama *OllamaClient, ragStore *RagStore, evalPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req evalRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}
		strategy := ChunkStrategy(req.Strategy)
		if strategy != ChunkStrategyFixed && strategy != ChunkStrategyStructural {
			writeError(w, http.StatusBadRequest, "strategy должен быть fixed_size или structural")
			return
		}

		idx, err := ragStore.Load()
		if err != nil {
			log.Printf("rag: load index failed: %v", err)
			writeError(w, http.StatusInternalServerError, "не удалось прочитать индекс")
			return
		}
		if idx == nil {
			writeError(w, http.StatusConflict, "индекс ещё не построен — сначала «Переиндексировать»")
			return
		}

		questions, err := loadEvalQuestions(evalPath)
		if err != nil {
			log.Printf("rag: load eval questions failed: %v", err)
			writeError(w, http.StatusInternalServerError, "не удалось прочитать контрольные вопросы: "+err.Error())
			return
		}

		// 20 LLM calls (no RAG + RAG per question) — generous timeout, same
		// reasoning as reindexHandler: a manual admin action, not a chat turn.
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Minute)
		defer cancel()

		results, err := RunEval(ctx, client, ollama, idx, questions, strategy, nil)
		if err != nil {
			writeRagQueryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, EvalRunResult{Strategy: strategy, Results: results})
	}
}

// evalRunStreamHandler is evalRunHandler with progress: same request body,
// same final EvalRunResult (as the "done" event), reporting each of the 20
// calls as it completes so the UI can show a real progress bar instead of
// a bare spinner for what's typically a multi-minute run.
func evalRunStreamHandler(client *LiteLLMClient, ollama *OllamaClient, ragStore *RagStore, evalPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req evalRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}
		strategy := ChunkStrategy(req.Strategy)
		if strategy != ChunkStrategyFixed && strategy != ChunkStrategyStructural {
			writeError(w, http.StatusBadRequest, "strategy должен быть fixed_size или structural")
			return
		}

		idx, err := ragStore.Load()
		if err != nil {
			log.Printf("rag: load index failed: %v", err)
			writeError(w, http.StatusInternalServerError, "не удалось прочитать индекс")
			return
		}
		if idx == nil {
			writeError(w, http.StatusConflict, "индекс ещё не построен — сначала «Переиндексировать»")
			return
		}

		questions, err := loadEvalQuestions(evalPath)
		if err != nil {
			log.Printf("rag: load eval questions failed: %v", err)
			writeError(w, http.StatusInternalServerError, "не удалось прочитать контрольные вопросы: "+err.Error())
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		stream := &sseWriter{w: w, rc: http.NewResponseController(w)}
		stream.err = stream.rc.Flush()

		// Not derived from r.Context(): same reasoning as streamAgentMessageHandler
		// — a closed tab shouldn't abort a run already a minute or two in.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()

		results, err := RunEval(ctx, client, ollama, idx, questions, strategy, func(p EvalProgress) {
			stream.send("progress", p)
		})
		if err != nil {
			stream.send("error", errorResponse{Error: err.Error()})
			return
		}
		stream.send("done", EvalRunResult{Strategy: strategy, Results: results})
	}
}

// evalRetrievalHandler is the secondary, no-LLM comparison: does top-K
// retrieval surface the expected source under each strategy, for every
// applicable control question.
func evalRetrievalHandler(ollama *OllamaClient, ragStore *RagStore, evalPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idx, err := ragStore.Load()
		if err != nil {
			log.Printf("rag: load index failed: %v", err)
			writeError(w, http.StatusInternalServerError, "не удалось прочитать индекс")
			return
		}
		if idx == nil {
			writeError(w, http.StatusConflict, "индекс ещё не построен — сначала «Переиндексировать»")
			return
		}

		questions, err := loadEvalQuestions(evalPath)
		if err != nil {
			log.Printf("rag: load eval questions failed: %v", err)
			writeError(w, http.StatusInternalServerError, "не удалось прочитать контрольные вопросы: "+err.Error())
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()

		result, err := RunRetrievalEval(ctx, ollama, idx, questions)
		if err != nil {
			writeRagQueryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// writeRagQueryError maps an Ollama or LiteLLM failure from the query/eval
// path to an HTTP response, same classification reindexHandler already
// uses for Ollama plus the existing LLM error mapping for the rest.
func writeRagQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrOllamaUnavailable) {
		writeError(w, http.StatusBadGateway, "Ollama недоступна: "+err.Error())
		return
	}
	writeLLMError(w, err)
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
