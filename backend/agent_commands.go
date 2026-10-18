package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// Day 31: two chat commands, each grounded deterministically instead of
// leaving retrieval or tool choice to the model — see their early intercept
// in PostMessage (agent_turn.go). Both finish through finishToolGroundedTurn
// below rather than the usual agentTurn JSON envelope: neither is a task
// estimate, and forcing the envelope's estimate/invariant fields onto them
// would just be noise the model has to fill with nulls correctly every time.

// defaultHelpQuestion is what a bare "/help" (no question of its own) asks
// the project-docs RAG index.
const defaultHelpQuestion = "Что ты умеешь и как устроен проект Work Intelligence Assistant?"

// parseHelpCommand reports whether msg is a /help command and the question
// to ground it with.
func parseHelpCommand(msg string) (question string, ok bool) {
	trimmed := strings.TrimSpace(msg)
	lower := strings.ToLower(trimmed)
	switch {
	case lower == "/help":
		return defaultHelpQuestion, true
	case strings.HasPrefix(lower, "/help "):
		q := strings.TrimSpace(trimmed[len("/help "):])
		if q == "" {
			q = defaultHelpQuestion
		}
		return q, true
	default:
		return "", false
	}
}

// defaultWorkSummaryDays is the period a bare "/work-summary" (no number)
// or an unparsable argument falls back to.
const defaultWorkSummaryDays = 7

// parseWorkSummaryCommand reports whether msg is a /work-summary command
// and the period, in days, to summarize.
func parseWorkSummaryCommand(msg string) (days int, ok bool) {
	fields := strings.Fields(strings.TrimSpace(msg))
	if len(fields) == 0 || strings.ToLower(fields[0]) != "/work-summary" {
		return 0, false
	}
	if len(fields) < 2 {
		return defaultWorkSummaryDays, true
	}
	n, err := strconv.Atoi(fields[1])
	if err != nil || n <= 0 {
		return defaultWorkSummaryDays, true
	}
	return n, true
}

// answerHelp grounds question in the project-docs RAG index (day 31) and
// returns a cited answer. It never falls back to the model's general
// knowledge about the project — the same "nothing found, say so" discipline
// as the standalone RAG endpoint's minConfidenceScore floor (rag_query.go).
func (a *Agent) answerHelp(ctx context.Context, client *LiteLLMClient, question string) (reply string, sources []RetrievedChunk, citations []Citation) {
	if a.docsRagStore == nil {
		return "Документация проекта пока не подключена к ассистенту.", nil, nil
	}
	idx, err := a.docsRagStore.Load()
	if err != nil {
		log.Printf("agent: docs rag index load failed: %v", err)
		return "Не удалось прочитать индекс документации проекта.", nil, nil
	}
	if idx == nil || len(idx.Chunks) == 0 {
		return "Индекс документации проекта пока не построен.", nil, nil
	}
	found := retrieveForDocs(ctx, a.docsRagOllama, idx, question)
	if len(found) == 0 {
		return "В документации проекта не нашлось ничего по этому вопросу.", nil, nil
	}
	result, err := callWithRetry(func() (ragLLMResult, error) { return answerDocsWithCitations(ctx, client, question, found) })
	if err != nil {
		log.Printf("agent: docs rag answer failed: %v", err)
		return "Не удалось сформировать ответ по документации проекта.", toRetrievedChunks(found), nil
	}
	return result.answer, toRetrievedChunks(found), result.citations
}

// workSummarySystemPrompt instructs the phrasing call to stick to exactly
// the data it was given — see concept.md's rule that an activity summary
// is facts only, never a productivity score, already followed by the
// existing weekly summary (weekly_summary.go).
func workSummarySystemPrompt(days int) string {
	return fmt.Sprintf(`Ты кратко пересказываешь пользователю его собственную рабочую активность за последние %d дней на русском языке, используя ТОЛЬКО переданные ниже данные аналитики (JSON) — не придумывай ничего сверх них. Упомяни общие часы, часы встреч, активные дни и проект с наибольшим временем, если они присутствуют в данных. Только факты — никаких оценок продуктивности и советов. 2-4 предложения, без markdown-разметки.`, days)
}

// answerWorkSummary forces a real call to the Worklog MCP server's
// get_analytics tool for [now-days, now] — unlike an ordinary chat
// question, the model never gets a choice whether to call it
// (agent_tools.go's routing is bypassed entirely). One follow-up LLM call
// phrases the raw numbers in Russian; it never invents numbers the tool
// didn't return.
func (a *Agent) answerWorkSummary(ctx context.Context, chatID string, client *LiteLLMClient, days int) (reply string, toolCalls []ToolCallRecord, err error) {
	conn := a.toolConn(ctx, "get_analytics")
	if conn == nil {
		return "Инструмент получения рабочей аналитики сейчас недоступен.", nil, nil
	}

	to := time.Now()
	from := to.AddDate(0, 0, -days)
	argsJSON, _ := json.Marshal(map[string]any{"from": from.Format("2006-01-02"), "to": to.Format("2006-01-02")})
	call := llmToolCall{}
	call.Function.Name = "get_analytics"
	call.Function.Arguments = string(argsJSON)

	record, resultText := a.runToolCall(ctx, chatID, conn, call)
	toolCalls = []ToolCallRecord{record}
	if !record.OK {
		return fmt.Sprintf("Не удалось получить рабочую аналитику за последние %d дней: %s", days, record.Error), toolCalls, nil
	}

	summary, err := client.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: workSummarySystemPrompt(days)},
		{Role: "user", Content: "Данные аналитики (JSON): " + resultText},
	}, 0.2, 1024, nil)
	if err != nil {
		return "", toolCalls, err
	}
	return summary, toolCalls, nil
}

// finishToolGroundedTurn stores a reply that was grounded deterministically
// (answerHelp/answerWorkSummary above) rather than through the usual
// agentTurn JSON envelope — the same bookkeeping as finishGracefulTurn
// (agent_turn.go), plus the ToolCalls/Sources/Citations a real turn stores
// on its assistant message (agent.go's AgentMessage) so the UI shows the
// same grounding evidence it would for a model-routed turn.
func (a *Agent) finishToolGroundedTurn(ctx context.Context, chat *Chat, branchID, userMessage, reply string, userSentAt time.Time, toolCalls []ToolCallRecord, sources []RetrievedChunk, citations []Citation) (*AgentReply, error) {
	assistantSentAt := time.Now()

	a.mu.Lock()
	chat.appendToBranch(branchID,
		AgentMessage{Role: "user", Content: userMessage, CreatedAt: userSentAt},
		AgentMessage{Role: "assistant", Content: reply, CreatedAt: assistantSentAt, ToolCalls: toolCalls, Sources: sources, Citations: citations},
	)
	chat.setLastMessageTaskState(branchID, computeTaskState(chat))
	if chat.Title == "Новый чат" {
		chat.Title = chatTitleFrom(userMessage)
	}
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s: %v", chat.ID, err)
	}

	chatID := chat.ID
	agentReply := a.buildAgentReplyLocked(chat, branchID, reply, nil, userSentAt, assistantSentAt)
	a.mu.Unlock()

	if event := a.compressHistoryIfDue(ctx, chatID); event != nil {
		agentReply.NewCompressionEvent = event
		a.mu.Lock()
		if c, ok := a.chats[chatID]; ok {
			agentReply.SummarizedMessageCount = c.SummarizedThrough
			agentReply.RawMessageCount = len(c.Messages) - c.SummarizedThrough
			agentReply.CumulativeTotalTokens = c.CumulativeTotalTokens
			agentReply.CumulativeCostUsd = c.CumulativeCostUsd
		}
		a.mu.Unlock()
	}
	return agentReply, nil
}
