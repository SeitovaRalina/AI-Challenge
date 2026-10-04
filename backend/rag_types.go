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
