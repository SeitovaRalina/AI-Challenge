package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Project-docs RAG grounds the /help chat command (agent_commands.go) in
// this project's own README and docs/ — a separate, much smaller index from
// the chat-history RAG (rag_index.go, day 21+), built once over static
// files instead of growing with every chat. Shares Chunk/RagIndex/RagStore
// and the retrieval/filtering helpers (rag_types.go, rag_query.go) — only
// the source corpus and chunking differ.

// mdHeadingRe matches a markdown ATX heading line ("# Title", "## Title",
// up to 6 levels) — the project's own docs are plain structured markdown,
// so splitting on headings gives each chunk a meaningful, citable section.
var mdHeadingRe = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)

// chunkMarkdown splits one file's content along its own headings — the
// "по структуре" strategy, same idea as chunkStructural (rag_chunk.go) but
// for markdown files instead of chat turns. A file with no heading at all
// (none of the project's docs, but defensive) becomes a single chunk.
func chunkMarkdown(path, content string) []Chunk {
	text := strings.TrimSpace(content)
	if text == "" {
		return nil
	}

	var chunks []Chunk
	addChunk := func(section, body string) {
		body = strings.TrimSpace(body)
		if body == "" {
			return
		}
		chunks = append(chunks, Chunk{
			ID: fmt.Sprintf("%s#%d", path, len(chunks)),
			// SessionID has no real session here — reused to carry the
			// doc's file path, the same role Title/Section play below, so
			// every chunk-formatting/retrieval helper built for chat
			// sessions (rag_query.go) works unchanged on doc chunks too.
			SessionID: path,
			Title:     path,
			Section:   section,
			Strategy:  ChunkStrategyStructural,
			Text:      body,
		})
	}

	matches := mdHeadingRe.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		addChunk("весь файл", text)
		return chunks
	}
	if matches[0][0] > 0 {
		addChunk("перед первым заголовком", text[:matches[0][0]])
	}
	for i, m := range matches {
		heading := strings.TrimSpace(text[m[2]:m[3]])
		bodyEnd := len(text)
		if i+1 < len(matches) {
			bodyEnd = matches[i+1][0]
		}
		addChunk(heading, heading+"\n"+text[m[1]:bodyEnd])
	}
	return chunks
}

// projectDocFiles reads README.md and every docs/*.md file under root
// (the repository root — see loadProjectDocsIndex in main.go for the exact
// path), keyed by a display path relative to root.
func projectDocFiles(root string) (map[string]string, error) {
	files := map[string]string{}

	readmePath := filepath.Join(root, "README.md")
	data, err := os.ReadFile(readmePath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", readmePath, err)
	}
	files["README.md"] = string(data)

	docsDir := filepath.Join(root, "docs")
	entries, err := os.ReadDir(docsDir)
	if err != nil {
		return nil, fmt.Errorf("read docs dir %s: %w", docsDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(docsDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read docs/%s: %w", e.Name(), err)
		}
		files["docs/"+e.Name()] = string(data)
	}
	return files, nil
}

// BuildDocsIndex chunks and embeds every project doc file under root — the
// same batched-embedding shape as BuildIndex (rag_index.go), just over a doc
// corpus instead of chats.
func BuildDocsIndex(ctx context.Context, root string, ollama *OllamaClient, embedModel string) (*RagIndex, error) {
	files, err := projectDocFiles(root)
	if err != nil {
		return nil, err
	}

	var allChunks []Chunk
	for path, content := range files {
		allChunks = append(allChunks, chunkMarkdown(path, content)...)
	}

	for start := 0; start < len(allChunks); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(allChunks) {
			end = len(allChunks)
		}
		batch := allChunks[start:end]
		texts := make([]string, len(batch))
		for i, c := range batch {
			texts[i] = c.Text
		}
		vectors, err := ollama.Embeddings(ctx, texts)
		if err != nil {
			return nil, fmt.Errorf("embedding doc chunks %d-%d: %w", start, end, err)
		}
		for i, v := range vectors {
			allChunks[start+i].Embedding = v
		}
	}

	return &RagIndex{
		BuiltAt:     time.Now(),
		SourceCount: len(files),
		EmbedModel:  embedModel,
		Chunks:      allChunks,
	}, nil
}

// retrieveForDocs mirrors retrieveForChat (rag_chat.go) over the docs index
// instead of the chat-history one.
func retrieveForDocs(ctx context.Context, ollama *OllamaClient, idx *RagIndex, question string) []scoredChunk {
	if idx == nil || strings.TrimSpace(question) == "" {
		return nil
	}
	vectors, err := ollama.Embeddings(ctx, []string{question})
	if err != nil {
		log.Printf("agent: docs rag grounding: embedding failed, answering without it: %v", err)
		return nil
	}
	candidates := retrieveTopK(idx.Chunks, ChunkStrategyStructural, vectors[0], defaultTopK)
	return filterByThreshold(candidates, minConfidenceScore)
}

// formatDocChunksForPrompt is formatChunksForPrompt (rag_query.go) with
// wording that fits a project-doc chunk ("файл", not "сессия").
func formatDocChunksForPrompt(scored []scoredChunk) string {
	var b strings.Builder
	for _, s := range scored {
		fmt.Fprintf(&b, "[%s] (файл %s, %s)\n%s\n\n", s.ID, s.SessionID, s.Section, s.Text)
	}
	return b.String()
}

// docsRagSystemPrompt is ragSystemPrompt (rag_query.go) re-worded for the
// project's own documentation instead of past task-estimation sessions —
// same {"answer","citations"} envelope, so it reuses ragLLMAnswer/
// verifyCitations unchanged.
const docsRagSystemPrompt = `Ты отвечаешь на вопрос о самом проекте Work Intelligence Assistant, используя приведённые ниже пронумерованные фрагменты из его документации (README и docs/). Отвечай на русском языке, опираясь только на эти фрагменты — не придумывай факты о проекте, которых там нет. Если фрагментов недостаточно для ответа, прямо скажи об этом. Отвечай кратко и по существу.

Текст поля "answer" рендерится как markdown в чате: одиночный перевод строки НЕ создаёт новую строку в интерфейсе. Если перечисляешь несколько пунктов, оформляй их настоящим markdown-списком — каждый пункт на своей строке, начинается с "- ", и пустая строка перед первым пунктом списка; никогда не используй символ "•" и не перечисляй пункты подряд в одном абзаце без переноса.

Ответь ТОЛЬКО одним JSON-объектом, без markdown-разметки и пояснений вокруг самого JSON (markdown внутри значения "answer" — это другое, см. выше), строго такого вида:
{"answer": "текст ответа на русском", "citations": [{"chunk_id": "ID фрагмента в квадратных скобках, из которого взята цитата", "text": "дословная цитата — точная подстрока текста этого фрагмента, без изменений и перефразирования"}]}

Каждая цитата должна быть ТОЧНОЙ подстрокой текста указанного фрагмента. Если фрагменты не подтверждают ни одного утверждения в ответе, оставь "citations" пустым массивом.`

// answerDocsWithCitations is answerWithCitations (rag_query.go) against the
// docs corpus and its own prompt — kept as its own small function rather
// than parameterizing the day-24 one, so this day's addition can't regress
// that already-working RAG-over-chat-history path.
func answerDocsWithCitations(ctx context.Context, client *LiteLLMClient, question string, final []scoredChunk) (ragLLMResult, error) {
	raw, err := client.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: docsRagSystemPrompt},
		{Role: "user", Content: fmt.Sprintf("Фрагменты из документации:\n\n%sВопрос: %s", formatDocChunksForPrompt(final), question)},
	}, 0.2, 8000, nil)
	if err != nil {
		return ragLLMResult{}, err
	}
	var parsed ragLLMAnswer
	if err := json.Unmarshal([]byte(stripCodeFences(raw)), &parsed); err != nil {
		return ragLLMResult{}, fmt.Errorf("docs rag answer: invalid response: %w (raw: %s)", err, truncateForLog(raw))
	}
	if parsed.Answer == "" {
		return ragLLMResult{}, fmt.Errorf("docs rag answer: empty answer field (raw: %s)", truncateForLog(raw))
	}
	return ragLLMResult{answer: parsed.Answer, citations: verifyCitations(parsed.Citations, final)}, nil
}

// AddDocsRagSource wires the project-docs index/embedder into the /help
// command — its own fields (not a.ragStore/a.ragOllama, day 25) so /help
// doesn't depend on the chat-history RAG having been wired up at all.
func (a *Agent) AddDocsRagSource(store *RagStore, ollama *OllamaClient) {
	a.docsRagStore = store
	a.docsRagOllama = ollama
}
