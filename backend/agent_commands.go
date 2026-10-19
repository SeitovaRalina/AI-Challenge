package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"aiwork/backend/internal/mcpclient"
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

// Day 32: /audit-timesheet reviews the draft timesheet for a period —
// overlaps with already-tracked Solidtime time, repositories with no
// explicit Solidtime project mapping, and implausible durations
// (timesheet_audit.go) — and finishes through the same
// finishToolGroundedTurn as /help and /work-summary above. It never
// writes anything to Solidtime.

const defaultAuditTimesheetDays = 7

// parseAuditTimesheetCommand reports whether msg is an /audit-timesheet
// command and the period, in days, to audit.
func parseAuditTimesheetCommand(msg string) (days int, ok bool) {
	fields := strings.Fields(strings.TrimSpace(msg))
	if len(fields) == 0 || strings.ToLower(fields[0]) != "/audit-timesheet" {
		return 0, false
	}
	if len(fields) < 2 {
		return defaultAuditTimesheetDays, true
	}
	n, err := strconv.Atoi(fields[1])
	if err != nil || n <= 0 {
		return defaultAuditTimesheetDays, true
	}
	return n, true
}

// callToolAsJSON calls tool via conn, decodes its structured result into
// out, and returns the resulting ToolCallRecord either way (so the caller
// can still show a failed call in the UI) — the chat-command counterpart
// to mcpCallJSON (timesheet_audit.go), which the REST audit endpoint uses
// instead since it has no chat message to attach a record to.
func (a *Agent) callToolAsJSON(ctx context.Context, chatID string, conn *mcpclient.Conn, tool string, args map[string]any, out any) (ToolCallRecord, error) {
	argsJSON, _ := json.Marshal(args)
	call := llmToolCall{}
	call.Function.Name = tool
	call.Function.Arguments = string(argsJSON)
	record, _ := a.runToolCall(ctx, chatID, conn, call)
	if !record.OK {
		return record, fmt.Errorf("%s: %s", tool, record.Error)
	}
	if len(record.Result) == 0 {
		return record, fmt.Errorf("%s: empty result", tool)
	}
	if err := json.Unmarshal(record.Result, out); err != nil {
		return record, fmt.Errorf("%s: decode result: %w", tool, err)
	}
	return record, nil
}

// maxAuditSessionsInReply caps how many flagged sessions the chat reply
// lists individually — the full set is still in Sessions on the stored
// tool-call results, this is only about keeping the reply readable.
const maxAuditSessionsInReply = 10

func auditFlagLabel(f AuditFlag) string {
	switch f {
	case FlagPossibleDuplicate:
		return "возможный дубль с Solidtime"
	case FlagUnmappedSolidtimeProject:
		return "нет привязки к проекту Solidtime"
	case FlagSuspiciousDuration:
		return "подозрительная длительность"
	default:
		return string(f)
	}
}

// formatAuditReply renders result as the deterministic Russian narrative
// the chat shows — counts first, then up to maxAuditSessionsInReply
// flagged sessions, each with its LLM remark when writeAuditRemarks
// succeeded, or its raw flag labels otherwise.
func formatAuditReply(result AuditResult) string {
	var b strings.Builder
	total := len(result.Sessions)
	ok := total - result.FlaggedCount
	fmt.Fprintf(&b, "За %s–%s собрано %d сессий. Без замечаний — %d. Требуют проверки — %d.",
		result.From.Format("02.01"), result.To.Format("02.01"), total, ok, result.FlaggedCount)
	for _, w := range result.Warnings {
		b.WriteString(" " + w + ".")
	}
	if result.FlaggedCount == 0 {
		return b.String()
	}
	// Chat messages render as markdown (components/markdown.tsx, no
	// remark-breaks) — a single "\n" is not a line break there, only a
	// real markdown list ("- " at the start of each line) is. A blank
	// line separates the list from the summary paragraph above, same
	// CommonMark reason.
	b.WriteString("\n\n")
	shown := 0
	for _, s := range result.Sessions {
		if len(s.Flags) == 0 {
			continue
		}
		if shown >= maxAuditSessionsInReply {
			fmt.Fprintf(&b, "\n… и ещё %d.", result.FlaggedCount-shown)
			break
		}
		label := s.Repo
		if s.Kind == "meeting" {
			label = "встреча «" + s.Title + "»"
		}
		remark := s.Remark
		if remark == "" {
			labels := make([]string, len(s.Flags))
			for i, f := range s.Flags {
				labels[i] = auditFlagLabel(f)
			}
			remark = strings.Join(labels, "; ")
		}
		if shown > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "- %s, %s–%s (%s) — %s", s.Start.Format("02.01"), s.Start.Format("15:04"), s.End.Format("15:04"), label, remark)
		shown++
	}
	return b.String()
}

// answerAuditTimesheet forces real calls to get_sessions,
// get_repo_solidtime_projects, and — when Solidtime is configured —
// list_time_entries: the model never chooses whether to call them, unlike
// an ordinary chat question. Flagging itself is deterministic
// (fetchAndAudit/flagSession, timesheet_audit.go); one follow-up LLM call
// only phrases a remark for the sessions already flagged, never deciding
// a flag on its own.
func (a *Agent) answerAuditTimesheet(ctx context.Context, chatID string, client *LiteLLMClient, days int) (reply string, toolCalls []ToolCallRecord, err error) {
	worklogConn := a.toolConn(ctx, "get_sessions")
	if worklogConn == nil {
		return "Инструмент получения сессий сейчас недоступен.", nil, nil
	}
	solidtimeConn := a.toolConn(ctx, "list_time_entries")

	to := time.Now()
	from := to.AddDate(0, 0, -days)

	callWorklog := func(tool string, args map[string]any, out any) error {
		record, callErr := a.callToolAsJSON(ctx, chatID, worklogConn, tool, args, out)
		toolCalls = append(toolCalls, record)
		return callErr
	}
	var callSolidtime func(tool string, args map[string]any, out any) error
	if solidtimeConn != nil {
		callSolidtime = func(tool string, args map[string]any, out any) error {
			record, callErr := a.callToolAsJSON(ctx, chatID, solidtimeConn, tool, args, out)
			toolCalls = append(toolCalls, record)
			return callErr
		}
	}

	result, err := fetchAndAudit(from, to, solidtimeConn != nil, callWorklog, callSolidtime)
	if err != nil {
		return "", toolCalls, err
	}

	if remarkErr := writeAuditRemarks(ctx, client, &result); remarkErr != nil {
		log.Printf("agent: chat %s: audit remarks failed, showing flags without them: %v", chatID, remarkErr)
	}

	return formatAuditReply(result), toolCalls, nil
}
