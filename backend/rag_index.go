package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// embedBatchSize caps how many chunk texts go into one Ollama /api/embed
// call — the corpus is small (a few hundred chunks at most) but batching
// still keeps any single request body/timeout reasonable.
const embedBatchSize = 64

// RagStore persists the single local RagIndex as one JSON file, the same
// pattern as weekly_summary.json — a sibling of the chats/labs/projects
// directories, not inside any of them.
type RagStore struct {
	path string
}

func NewRagStore(path string) *RagStore {
	return &RagStore{path: path}
}

// Load returns (nil, nil) if no index has been built yet — day 21's GET
// /api/rag/index must distinguish "not built" from "build failed".
func (s *RagStore) Load() (*RagIndex, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var idx RagIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func (s *RagStore) Save(idx *RagIndex) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

// BuildIndex chunks every eligible chat with both strategies, embeds every
// chunk via Ollama, and returns the resulting index — it does not save it,
// so a caller can inspect/discard a failed build without corrupting the
// previous on-disk index.
//
// A chat is "eligible" when it actually produced an estimate (Estimate !=
// nil) and isn't a day-9/day-10 context-strategy lab chat (LabID == "") —
// this is the corpus decision from the week-5 plan: real historical
// task-estimation sessions, not every chat ever created in this instance.
func BuildIndex(ctx context.Context, chats []*Chat, ollama *OllamaClient, embedModel string) (*RagIndex, error) {
	var allChunks []Chunk
	sourceCount := 0
	for _, c := range chats {
		if c.LabID != "" || c.Estimate == nil {
			continue
		}
		sourceCount++
		allChunks = append(allChunks, buildChunksForChat(c)...)
	}

	for start := 0; start < len(allChunks); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(allChunks) {
			end = len(allChunks)
		}
		batch := allChunks[start:end]

		texts := make([]string, len(batch))
		for i, ch := range batch {
			texts[i] = ch.Text
		}

		vectors, err := ollama.Embeddings(ctx, texts)
		if err != nil {
			return nil, fmt.Errorf("embedding chunks %d-%d: %w", start, end, err)
		}
		for i, v := range vectors {
			allChunks[start+i].Embedding = v
		}
	}

	return &RagIndex{
		BuiltAt:     time.Now(),
		SourceCount: sourceCount,
		EmbedModel:  embedModel,
		Chunks:      allChunks,
	}, nil
}

// ComputeStats groups idx's chunks by strategy for the day-21 comparison —
// chunk count and average length per strategy, over the same corpus.
func ComputeStats(idx *RagIndex) []StrategyStats {
	totals := map[ChunkStrategy]*StrategyStats{}
	order := []ChunkStrategy{ChunkStrategyFixed, ChunkStrategyStructural}
	for _, s := range order {
		totals[s] = &StrategyStats{Strategy: s}
	}

	for _, ch := range idx.Chunks {
		st, ok := totals[ch.Strategy]
		if !ok {
			st = &StrategyStats{Strategy: ch.Strategy}
			totals[ch.Strategy] = st
			order = append(order, ch.Strategy)
		}
		st.ChunkCount++
		st.TotalChars += len([]rune(ch.Text))
	}

	stats := make([]StrategyStats, 0, len(order))
	for _, s := range order {
		st := *totals[s]
		if st.ChunkCount > 0 {
			st.AvgChars = float64(st.TotalChars) / float64(st.ChunkCount)
		}
		stats = append(stats, st)
	}
	return stats
}
