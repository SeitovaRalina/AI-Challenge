package main

import (
	"fmt"
	"strings"
)

// fixedChunkSize/fixedChunkOverlap are the fixed-size strategy's window, in
// runes — small enough that a 500-2500 char session yields several chunks
// (needed for a meaningful comparison against the structural strategy),
// with enough overlap that a sentence split across a window boundary still
// appears whole in at least one chunk.
const (
	fixedChunkSize    = 500
	fixedChunkOverlap = 100
)

// sessionFullText flattens one chat into a single blob of text — title,
// every message, and (once present) the final estimate's fields — in
// reading order, for the fixed-size strategy to window over blind to any
// structure.
func sessionFullText(c *Chat) string {
	var b strings.Builder
	b.WriteString(c.Title)
	b.WriteString("\n\n")
	for _, m := range c.Messages {
		b.WriteString(m.Content)
		b.WriteString("\n\n")
	}
	if c.Estimate != nil {
		b.WriteString(estimateText(c.Estimate))
	}
	return b.String()
}

// estimateText renders an estimate's fields as plain text, shared by both
// chunking strategies (sessionFullText folds it into the fixed-size blob;
// chunkStructural also chunks it field by field).
func estimateText(e *EstimateResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nКатегория: %s. Сложность: %s. Оценка: %.0f-%.0f ч.\n",
		e.Summary, e.Category, e.Complexity, e.EstimatedHoursMin, e.EstimatedHoursMax)
	for _, r := range e.Risks {
		fmt.Fprintf(&b, "Риск: %s\n", r)
	}
	for _, a := range e.Assumptions {
		fmt.Fprintf(&b, "Допущение: %s\n", a)
	}
	for _, s := range e.Subtasks {
		fmt.Fprintf(&b, "Подзадача: %s (%.0f-%.0f ч.)\n", s.Description, s.EstimatedHoursMin, s.EstimatedHoursMax)
	}
	return b.String()
}

// chunkFixedSize splits text into overlapping windows of fixedChunkSize
// runes, with no regard for sentence/field boundaries — the baseline
// strategy day 21 compares against chunkStructural. Runes, not bytes, so a
// window boundary never lands inside a multi-byte Cyrillic character.
func chunkFixedSize(text string) []string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return nil
	}
	step := fixedChunkSize - fixedChunkOverlap
	var chunks []string
	for start := 0; start < len(runes); start += step {
		end := start + fixedChunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunk := strings.TrimSpace(string(runes[start:end]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		if end == len(runes) {
			break
		}
	}
	return chunks
}

// structuralSection is one field-level chunk chunkStructural produces,
// named after where it came from in the chat (title, a specific message, or
// one estimate field) — the "по структуре" counterpart to chunkFixedSize's
// blind windows.
type structuralSection struct {
	Section string
	Text    string
}

// chunkStructural splits a chat along its own natural structure — one chunk
// per message turn, one per estimate field — instead of a fixed window. A
// session with a long clarifying dialogue yields many small chunks here but
// the same handful of fixed-size windows there, which is the point of
// comparing the two.
func chunkStructural(c *Chat) []structuralSection {
	var sections []structuralSection
	if strings.TrimSpace(c.Title) != "" {
		sections = append(sections, structuralSection{Section: "title", Text: c.Title})
	}
	for i, m := range c.Messages {
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		sections = append(sections, structuralSection{
			Section: fmt.Sprintf("message[%d].%s", i, m.Role),
			Text:    text,
		})
	}
	if c.Estimate != nil {
		e := c.Estimate
		if e.Summary != "" {
			sections = append(sections, structuralSection{Section: "estimate.summary", Text: e.Summary})
		}
		sections = append(sections, structuralSection{
			Section: "estimate.overview",
			Text:    fmt.Sprintf("Категория: %s. Сложность: %s. Оценка: %.0f-%.0f ч.", e.Category, e.Complexity, e.EstimatedHoursMin, e.EstimatedHoursMax),
		})
		for i, r := range e.Risks {
			sections = append(sections, structuralSection{Section: fmt.Sprintf("estimate.risks[%d]", i), Text: r})
		}
		for i, a := range e.Assumptions {
			sections = append(sections, structuralSection{Section: fmt.Sprintf("estimate.assumptions[%d]", i), Text: a})
		}
		for i, s := range e.Subtasks {
			sections = append(sections, structuralSection{
				Section: fmt.Sprintf("estimate.subtasks[%d]", i),
				Text:    fmt.Sprintf("%s (%.0f-%.0f ч.)", s.Description, s.EstimatedHoursMin, s.EstimatedHoursMax),
			})
		}
	}
	if c.Task != nil {
		if c.Task.Goal != "" {
			sections = append(sections, structuralSection{Section: "task.goal", Text: c.Task.Goal})
		}
		for i, con := range c.Task.Constraints {
			sections = append(sections, structuralSection{Section: fmt.Sprintf("task.constraints[%d]", i), Text: con})
		}
	}
	return sections
}

// buildChunksForChat runs both strategies over one chat and returns every
// resulting Chunk, metadata filled in but Embedding still nil.
func buildChunksForChat(c *Chat) []Chunk {
	var chunks []Chunk

	for i, text := range chunkFixedSize(sessionFullText(c)) {
		chunks = append(chunks, Chunk{
			ID:        fmt.Sprintf("%s-fixed-%d", c.ID, i),
			SessionID: c.ID,
			Title:     c.Title,
			Section:   fmt.Sprintf("window[%d]", i),
			Strategy:  ChunkStrategyFixed,
			Text:      text,
		})
	}

	for i, s := range chunkStructural(c) {
		chunks = append(chunks, Chunk{
			ID:        fmt.Sprintf("%s-struct-%d", c.ID, i),
			SessionID: c.ID,
			Title:     c.Title,
			Section:   s.Section,
			Strategy:  ChunkStrategyStructural,
			Text:      s.Text,
		})
	}

	return chunks
}
