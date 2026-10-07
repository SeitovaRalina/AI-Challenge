package main

import "time"

// ChunkStrategy is one of the two chunking approaches day 21 compares — see
// rag_chunk.go for both implementations.
type ChunkStrategy string

const (
	ChunkStrategyFixed      ChunkStrategy = "fixed_size"
	ChunkStrategyStructural ChunkStrategy = "structural"
)

// Chunk is one indexed piece of text plus the metadata needed to show where
// it came from (source session, section) and tell retrieved results apart
// (source/title/section/chunk_id, per the day-21 "усиление"). Embedding is
// nil until EmbedChunks has run.
type Chunk struct {
	ID        string        `json:"id"`
	SessionID string        `json:"session_id"`
	Title     string        `json:"title"`
	Section   string        `json:"section"`
	Strategy  ChunkStrategy `json:"strategy"`
	Text      string        `json:"text"`
	Embedding []float32     `json:"embedding"`
}

// RagIndex is the full local index persisted to disk: every chunk from
// every indexed session, both strategies side by side so day 22+ queries
// can pick one and day 21's comparison can read both.
type RagIndex struct {
	BuiltAt     time.Time `json:"built_at"`
	SourceCount int       `json:"source_count"`
	EmbedModel  string    `json:"embed_model"`
	Chunks      []Chunk   `json:"chunks"`
}

// StrategyStats summarizes one chunking strategy's output for the day-21
// comparison — chunk count and average/total length are enough to see the
// two strategies actually differ, without needing retrieval quality yet
// (that comparison is day 22+, once there's something to query).
type StrategyStats struct {
	Strategy   ChunkStrategy `json:"strategy"`
	ChunkCount int           `json:"chunk_count"`
	TotalChars int           `json:"total_chars"`
	AvgChars   float64       `json:"avg_chars"`
}

// ChunkPreview is one chunk without its embedding (a 768-number vector is
// useless to read by eye) — what GET /api/rag/chunks returns for someone
// inspecting the index's actual content, as opposed to IndexStatus's
// aggregate counts.
type ChunkPreview struct {
	ID        string        `json:"id"`
	SessionID string        `json:"session_id"`
	Title     string        `json:"title"`
	Section   string        `json:"section"`
	Strategy  ChunkStrategy `json:"strategy"`
	Text      string        `json:"text"`
}

// IndexStatus is what GET /api/rag/index returns: whether an index exists,
// when it was built, and the per-strategy comparison.
type IndexStatus struct {
	Exists      bool            `json:"exists"`
	BuiltAt     *time.Time      `json:"built_at,omitempty"`
	SourceCount int             `json:"source_count"`
	EmbedModel  string          `json:"embed_model,omitempty"`
	Strategies  []StrategyStats `json:"strategies"`
}

// RetrievedChunk is one chunk retrieval matched for a question, with its
// similarity score — what both the single-query endpoint and the eval
// runs report so the UI can show exactly what grounded an answer.
type RetrievedChunk struct {
	SessionID string  `json:"session_id"`
	Title     string  `json:"title"`
	Section   string  `json:"section"`
	ChunkID   string  `json:"chunk_id"`
	Score     float64 `json:"score"`
	// Text is the chunk's actual content — included so the UI can show
	// exactly what grounded the answer without a second round trip (user
	// feedback: sources need to be inspectable, not just named).
	Text string `json:"text"`
}

// RagAnswer is what POST /api/rag/query returns for either mode — Strategy
// and Retrieved stay empty for mode "no_rag", which never touches the
// index at all (the honest baseline day 22 compares RAG against).
type RagAnswer struct {
	Mode     string        `json:"mode"`
	Strategy ChunkStrategy `json:"strategy,omitempty"`
	Answer   string        `json:"answer"`
	// Retrieved is explicitly `[]` (not omitted) when filtering legitimately
	// drops every candidate — "0 chunks passed" is a meaningful result the
	// UI must be able to tell apart from "this field doesn't apply" (mode
	// no_rag, where it really is omitted below in Query).
	Retrieved []RetrievedChunk `json:"retrieved"`
	// Day 23 additions, zero/empty unless the request opted into them.
	RewrittenQuestion string `json:"rewritten_question,omitempty"`
	// CandidateCount/FilteredCount are "топ-K до и после фильтрации":
	// CandidateCount is how many chunks were considered (the cosine pool,
	// widened for rerank), FilteredCount how many survived the min_score
	// cutoff and actually grounded the answer — both NOT omitempty, since 0
	// is a meaningful, distinct result ("everything got filtered out"), not
	// the same as "this field wasn't computed".
	CandidateCount int `json:"candidate_count"`
	FilteredCount  int `json:"filtered_count"`
}

// EvalQuestion is one of the day-22 "10 контрольных вопросов" — hand-
// written against this instance's real corpus (see backend/data/
// rag_eval.json, gitignored like the rest of data/). ExpectedSources is
// empty when the question is an aggregate across many sessions or has no
// good match in the corpus at all ("если применимо" per the assignment).
type EvalQuestion struct {
	Question        string   `json:"question"`
	Expectation     string   `json:"expectation"`
	ExpectedSources []string `json:"expected_sources"`
}

// EvalQuestionResult is one eval question answered without RAG and with
// baseline RAG (no rerank/rewrite/filter) always; ImprovedRagAnswer is
// populated too, alongside them, when the run's RagOptions had at least
// one enhancement on — one run, up to three comparable answers, not two
// separate runs the caller has to eyeball against each other.
type EvalQuestionResult struct {
	EvalQuestion
	NoRagAnswer         string           `json:"no_rag_answer"`
	RagAnswer           string           `json:"rag_answer"`
	Retrieved           []RetrievedChunk `json:"retrieved"`
	ExpectedSourceHit   bool             `json:"expected_source_hit"`
	ExpectedSourceCheck bool             `json:"expected_source_check"` // false when ExpectedSources was empty — nothing to check
	// Set only when the run requested rerank/rewrite/filtering.
	ImprovedRagAnswer string           `json:"improved_rag_answer,omitempty"`
	ImprovedRetrieved []RetrievedChunk `json:"improved_retrieved,omitempty"`
}

// EvalRunResult is the full day-22 RAG-vs-no-RAG run, all 10 questions,
// RAG answered with one chosen strategy.
type EvalRunResult struct {
	Strategy ChunkStrategy        `json:"strategy"`
	Results  []EvalQuestionResult `json:"results"`
}

// RetrievalHitRate is one strategy's score on the retrieval-only
// comparison (RunRetrievalEval) — whether top-K retrieval actually
// surfaces each question's expected source session, no LLM call involved.
// This is the day-21-leftover comparison the plan moved to day 22 (a
// question isn't chunked, it's matched against existing chunks, which is
// retrieval, not chunking) — secondary to EvalRunResult above, not the
// day-22 headline.
type RetrievalHitRate struct {
	Strategy ChunkStrategy `json:"strategy"`
	Hits     int           `json:"hits"`
	Total    int           `json:"total"` // only questions with ExpectedSources set
	HitRate  float64       `json:"hit_rate"`
}

// RetrievalQuestionResult is one question's per-strategy hit/miss, for the
// 2-column comparison table.
type RetrievalQuestionResult struct {
	Question string                 `json:"question"`
	Checked  bool                   `json:"checked"` // false when the question has no ExpectedSources
	Hits     map[ChunkStrategy]bool `json:"hits"`
}

type RetrievalEvalResult struct {
	Questions  []RetrievalQuestionResult `json:"questions"`
	Strategies []RetrievalHitRate        `json:"strategies"`
}
