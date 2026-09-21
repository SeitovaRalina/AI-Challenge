package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// agentSystemPrompt wraps the day-1 estimate schema (systemPrompt) with
// conversational behavior: the first user message is treated as a task
// description and produces an initial estimate, and every later message is
// answered using the full conversation history, only re-issuing (and
// overwriting) the estimate when the user's message actually changes it.
const agentSystemPrompt = systemPrompt + `

You are embedded in a chat interface, not a one-shot form. Always answer in
this envelope, as a single JSON object with no markdown fences and no text
outside it:

{
  "reply": "the message shown to the user in the chat, in Russian, conversational, may reference the estimate but does not need to repeat every field",
  "estimate": <the EstimateResponse object described above> or null,
  "invariant_conflict": ["verbatim text of each violated project invariant"] or [] when there is no conflict, or no invariants are configured
}

This applies even to a plain conversational answer that changes nothing (a
clarifying question, summing existing subtask hours, small talk) — NEVER
respond with bare prose outside this envelope, even then; put that prose in
"reply" and set "estimate" to null.

"invariant_conflict" is only ever non-empty when the project's hard
invariants (given to you as their own system message, when any exist) rule
out what the user is asking for — see that message for the exact rules on
when and how to refuse. Otherwise always set it to [].

Set "estimate" to a full, updated EstimateResponse object only when this
message is the task description itself, or when the user's message changes
the estimate (new details, constraints, or an explicit request to redo it).
Otherwise set "estimate" to null and use "reply" for ordinary conversation
(answering questions, discussing the task, clarifying assumptions) — do not
invent a new estimate on every turn just because one exists already.

Whenever you do set "estimate", additionally include a "subtasks" field: an
array of {"name", "description", "estimated_hours_min", "estimated_hours_max"}
breaking the task into its logical pieces. Only break a task down when it is
actually large enough to benefit from that (roughly, when your own total
estimate is beyond a day or two of work, or the task clearly bundles several
distinct pieces of work) — for a small, atomic task, "subtasks" must be an
empty array rather than artificially split into filler pieces. The top-level
"estimated_hours_min"/"estimated_hours_max" MUST equal the sum of all
subtasks' own min/max hours — never a separately guessed number. Every "reply"
you write afterward, including when "estimate" is null, MUST stay consistent
with the current subtasks: if the user asks how long a specific subtask will
take, answer from its exact estimated_hours_min/estimated_hours_max instead
of guessing.`

// agentTurn is the envelope the agent asks the model for on every turn.
type agentTurn struct {
	Reply             string            `json:"reply"`
	Estimate          *EstimateResponse `json:"estimate"`
	InvariantConflict []string          `json:"invariant_conflict"`
}

// interviewModeSystemPrompt is injected, turn-only, when the frontend flags
// a message as part of day 12's onboarding interview (see
// ONBOARDING_KICKOFF... no — see chat-panel.tsx's INTERVIEW_STEPS). It
// exists only in this one call's messages slice: never appended to
// chat.Messages, never part of userMessage/assistantReply, so neither
// updateTaskMemory nor updateProfile (both take only those two plain
// strings — see memory_task.go/memory_profile.go) ever see it, can't
// capture it, and can't replay it back into a later turn. That's the exact
// failure mode that broke the main call outright when an earlier design put
// equivalent instructions inside a real user message instead (day 11's
// task-memory extraction captured them as "task constraints" and
// re-injected them next turn) — routing the override through a system
// message that lives for one call only closes that off structurally, not by
// carefully wording it.
//
// Needed at all because agentSystemPrompt's own persona keeps steering
// every reply back toward "опишите задачу" — confirmed live: mid-interview
// turns like "Меня зовут Раля." got replies asking for a task to estimate,
// which reads as the assistant ignoring what was just said.
const interviewModeSystemPrompt = `Пользователь сейчас отвечает на вопросы короткого интервью для заполнения своего личного профиля (имя, стек, стиль общения, формат ответов, ограничения) — вопросы ему показывает интерфейс, не ты. Его текущее сообщение — просто ответ на один такой вопрос, не начало задачи на оценку.

Тепло и коротко прими то, что он сказал, в 1-2 предложениях. НЕ предлагай оценить задачу, НЕ спрашивай "какую задачу оценить" и не упоминай оценку вовсе — до конца интервью никаких task-оценок. Оценка задач начнётся сама, когда пользователь заговорит о конкретной задаче.`

// PostMessage appends the user's message to chatID's active branch (or its
// plain history, for every strategy but branching), asks the LLM for a reply
// using that strategy's own view of the conversation so far, and stores the
// assistant's reply back into that same place. interviewMode injects
// interviewModeSystemPrompt for this call only — see its own doc comment.
func (a *Agent) PostMessage(ctx context.Context, chatID, userMessage string, interviewMode bool) (*AgentReply, error) {
	a.mu.Lock()
	chat, ok := a.chats[chatID]
	if !ok {
		a.mu.Unlock()
		return nil, ErrChatNotFound
	}
	// Snapshot everything PostMessage needs under the lock, then release it
	// for the (slow) LLM call — a chat is only ever driven by one user (or,
	// for a lab's fan-out, one background goroutine per sibling chat), so
	// this is safe.
	history := append([]AgentMessage(nil), chat.activeMessages()...)
	currentEstimate := chat.activeEstimate()
	lastContextTokens := chat.activeLastContextTokens()
	strategy := chat.ContextStrategy
	facts := chat.Facts
	summary := chat.Summary
	summarizedThrough := chat.SummarizedThrough
	task := chat.Task
	// Snapshot BEFORE this turn runs — the stage as the conversation stood
	// when the user sent this message, used only for prompt injection below.
	// buildAgentReplyLocked recomputes it fresh AFTER the turn mutates chat,
	// for what the reply/UI reports.
	taskStateBefore := computeTaskState(chat)
	labID := chat.LabID
	projectID := chat.ProjectID
	var project *Project
	if projectID != "" {
		project = a.projects[projectID]
	}
	profile := a.profile
	// Captured once, up front — this turn's result must land on the branch
	// it was actually asked about even if SetActiveBranch runs while the
	// (slow) LLM call below is in flight; re-reading chat.ActiveBranchID
	// after the call would silently redirect the reply onto whatever branch
	// the user has switched to in the meantime.
	branchID := chat.ActiveBranchID
	a.mu.Unlock()

	log.Printf("agent: chat %s: turn %d, strategy %s, message length %d", chatID, len(history)/2+1, strategy, len(userMessage))

	userSentAt := time.Now()

	// Pre-call overflow guard: the previous turn's prompt_tokens PLUS its own
	// completion_tokens is what's actually sitting in context right now — the
	// reply the model just wrote is appended to the conversation and will
	// itself be sent back as part of the prompt on the next call, so it
	// already occupies context space even though it was never sent as a
	// prompt yet. This turn's history is at most a couple of messages larger
	// than that — close enough to treat as this turn's starting budget.
	// Rather than let the model write an unbounded reply and only check the
	// total afterward, the real call's max_tokens is capped to whatever room
	// is left, so the upstream API itself truncates (finish_reason "length")
	// if the answer would need more room than remains. Below
	// minCompletionBudget there isn't enough room left for a coherent reply,
	// so that case still skips the call entirely.
	const minCompletionBudget = 64
	remainingBudget := 0
	if a.contextTokenLimit > 0 {
		remainingBudget = a.contextTokenLimit - lastContextTokens
	}
	if a.contextTokenLimit > 0 && remainingBudget < minCompletionBudget {
		log.Printf("agent: chat %s: context limit reached (%d/%d tokens, %d left), skipping LLM call",
			chatID, lastContextTokens, a.contextTokenLimit, remainingBudget)
		reply := contextFullReplyText(lastContextTokens, a.contextTokenLimit)
		return a.finishGracefulTurn(ctx, chat, branchID, userMessage, reply, userSentAt, nil)
	}

	messages := make([]chatMessage, 0, len(history)+4)
	messages = append(messages, chatMessage{Role: "system", Content: agentSystemPrompt})
	if interviewMode {
		messages = append(messages, chatMessage{Role: "system", Content: interviewModeSystemPrompt})
	}
	// Re-stating the exact current estimate (subtasks included) as its own
	// system message means a question like "how long will X take" is answered
	// from these precise numbers, not from however the prior reply phrased it.
	if currentEstimate != nil {
		if encoded, err := json.Marshal(currentEstimate); err == nil {
			messages = append(messages, chatMessage{
				Role:    "system",
				Content: "Текущая актуальная оценка задачи (JSON, используй точные числа при ответах про сроки): " + string(encoded),
			})
		}
	}
	// Profile (day 12) is deliberately withheld for lab chats, same reasoning
	// as the labID=="" gate around the side-calls below: AnalyzeLab's token/
	// cost comparison must stay about the ContextStrategy being tested, not an
	// extra always-on system message none of the pre-day-12 runs had.
	injectedProfile := profile
	if labID != "" {
		injectedProfile = nil
	}
	messages = append(messages, buildMemorySystemMessages(task, project, injectedProfile)...)
	// Task state (day 13) is withheld for lab chats for the same reason
	// profile is: day 10's strategy comparison must stay free of anything
	// beyond the ContextStrategy actually being tested.
	if labID == "" {
		messages = append(messages, chatMessage{Role: "system", Content: taskStateSystemPrompt(taskStateBefore)})
	}
	extra, raw := buildContextMessages(strategy, a.historyKeepLastN, history, facts, summary, summarizedThrough)
	messages = append(messages, extra...)
	for _, m := range raw {
		messages = append(messages, chatMessage{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, chatMessage{Role: "user", Content: userMessage})

	maxTokens := 0
	if a.contextTokenLimit > 0 {
		maxTokens = remainingBudget
	}

	callStart := time.Now()
	completion, err := a.client.doChatCompletion(ctx, a.client.model, messages, 0.2, maxTokens, nil)
	if err != nil {
		// A real upstream context-length rejection is handled the same
		// gracefully-in-chat way as the pre-call guard above, instead of
		// surfacing as a 502 to the user.
		if errors.Is(err, ErrContextOverflow) {
			log.Printf("agent: chat %s: model rejected the request as over its context length: %v", chatID, err)
			reply := contextFullReplyText(lastContextTokens, a.contextTokenLimit)
			return a.finishGracefulTurn(ctx, chat, branchID, userMessage, reply, userSentAt, nil)
		}
		log.Printf("agent: chat %s: LLM call failed after %s: %v", chatID, time.Since(callStart).Round(time.Millisecond), err)
		return nil, err
	}
	finishReason := completion.Choices[0].FinishReason
	log.Printf("agent: chat %s: LLM call took %s (finish_reason=%s)", chatID, time.Since(callStart).Round(time.Millisecond), finishReason)
	if finishReason == "length" {
		log.Printf("agent: chat %s: response truncated by the %d-token context budget — real API enforced the simulated context window", chatID, maxTokens)
	}

	turn, err := parseAgentTurn(completion.Choices[0].Message.Content)
	if err != nil {
		// A reasoning model can spend its entire truncated budget on hidden
		// reasoning and never emit any visible content at all — worse than a
		// mid-sentence cutoff, there's nothing to fall back to as prose
		// either. This is NOT the dialog running out of room — it's this one
		// reply that needed more than the remaining per-turn budget, so it
		// gets its own honest message instead of the "context limit reached"
		// one.
		if finishReason == "length" {
			log.Printf("agent: chat %s: truncated response was unusable (finish_reason=length, %d-token budget): %v", chatID, maxTokens, err)
			// The call genuinely happened and cost real tokens — usage is
			// still populated even though the content came out unusable, so
			// the chat's running totals must reflect it.
			usage := tokenUsageFrom(completion.Usage)
			reply := truncatedReplyText(maxTokens)
			return a.finishGracefulTurn(ctx, chat, branchID, userMessage, reply, userSentAt, usage)
		}
		log.Printf("agent: chat %s: failed to parse LLM turn: %v; raw response: %s", chatID, err, truncateForLog(completion.Choices[0].Message.Content))
		return nil, err
	}
	assistantSentAt := time.Now()

	usage := tokenUsageFrom(completion.Usage)
	if usage != nil {
		log.Printf("agent: chat %s: usage prompt=%d completion=%d total=%d",
			chatID, usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
	}

	a.mu.Lock()

	chat.appendToBranch(branchID,
		AgentMessage{Role: "user", Content: userMessage, CreatedAt: userSentAt, Usage: usage},
		AgentMessage{Role: "assistant", Content: turn.Reply, CreatedAt: assistantSentAt, Usage: usage, InvariantConflict: turn.InvariantConflict},
	)
	if turn.Estimate != nil {
		chat.setBranchEstimate(branchID, turn.Estimate)
		log.Printf("agent: chat %s: estimate updated, %.1f-%.1fh, %d subtask(s)",
			chatID, turn.Estimate.EstimatedHoursMin, turn.Estimate.EstimatedHoursMax, len(turn.Estimate.Subtasks))
		// A new or changed estimate always un-accepts a prior acceptance —
		// see task_state.go's estimated<->done transition.
		chat.EstimateRevisions++
		chat.TaskDone = false
	}
	// Stamped after the estimate mutation above (and after appendToBranch),
	// so it reflects this turn's actual outcome, not the pre-turn snapshot
	// (taskStateBefore) used for prompt injection.
	chat.setLastMessageTaskState(branchID, computeTaskState(chat))
	if chat.Title == "Новый чат" {
		chat.Title = chatTitleFrom(userMessage)
	}
	if usage != nil {
		chat.setBranchLastContextTokens(branchID, usage.TotalTokens)
		chat.addUsage(usage)
	}

	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s: %v", chat.ID, err)
	}

	agentReply := a.buildAgentReplyLocked(chat, branchID, turn.Reply, usage, userSentAt, assistantSentAt)
	agentReply.InvariantConflict = turn.InvariantConflict
	a.mu.Unlock()

	// Runs its own (possibly slow) LLM calls outside the lock just released,
	// so a strategy side-call for this chat never blocks any other chat.
	// agentReply's strategy fields were captured BEFORE these calls, so if
	// one actually changes something, refresh them afterward — otherwise
	// this turn's own response would understate what just happened to its
	// own chat.
	if strategy == StrategyStickyFacts {
		if updated := a.updateFactsAfterTurn(ctx, chatID, userMessage, turn.Reply); updated != nil {
			a.mu.Lock()
			if c, ok := a.chats[chatID]; ok {
				agentReply.Facts = c.Facts
				agentReply.CumulativeTotalTokens = c.CumulativeTotalTokens
				agentReply.CumulativeCostUsd = c.CumulativeCostUsd
			}
			a.mu.Unlock()
		}
	}
	// Working/long-term memory (day 11) are independent of ContextStrategy —
	// they run for every non-lab chat, not just sticky_facts. A lab's
	// strategy chats are skipped entirely: AnalyzeLab's token/cost comparison
	// is specifically about the strategy being tested, and an extra uniform
	// side-call on all of them would pollute those numbers with something
	// unrelated to that comparison.
	if labID == "" {
		// Run concurrently, not sequentially: two independent LLM calls
		// (this chat's working memory, this project's long-term memory)
		// chained one after another was observed stacking enough latency to
		// blow past the 60s budget postAgentMessageHandler gives the whole
		// turn — running them side by side keeps the added latency to
		// whichever of the two is slower, not their sum.
		//
		// memCtx is deliberately its own context.WithTimeout(context.Background(), ...),
		// NOT derived from ctx (the request's, itself bounded to 60s total by
		// postAgentMessageHandler): on a slow gateway day the MAIN call alone
		// was observed taking 34-50s, leaving as little as 10s of that shared
		// 60s for both memory calls together — nowhere near enough, and both
		// silently failed (by design — a memory-update failure never fails
		// the user's own turn) every single time. Memory updates get a fresh,
		// independent budget instead, so a slow main call never starves them.
		var wg sync.WaitGroup
		var updatedTask *TaskMemory
		var updatedProject *Project
		var updatedProfile *UserProfile
		var invariantDiff *InvariantDiff

		memCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		wg.Add(1)
		go func() {
			defer wg.Done()
			updatedTask = a.updateTaskMemoryAfterTurn(memCtx, chatID, userMessage, turn.Reply)
		}()
		if projectID != "" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				updatedProject = a.updateProjectMemoryAfterTurn(memCtx, chatID, projectID, userMessage, turn.Reply)
			}()
			// Day 14: a separate side-call from KnownStack/Notes above, with
			// its own much stricter extraction prompt — mixing the two
			// thresholds into one call would blur "reference memory" with
			// "hard rule". Both mutate the same *Project but disjoint fields
			// (Invariants vs KnownStack/Notes) under a.mu each, so running
			// them concurrently is safe; see the re-fetch below for why
			// agentReply.Project can't just take either call's own copy.
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, invariantDiff = a.updateInvariantsAfterTurn(memCtx, chatID, projectID, userMessage, turn.Reply)
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			updatedProfile = a.updateProfileAfterTurn(memCtx, chatID, userMessage, turn.Reply)
		}()
		wg.Wait()

		if updatedTask != nil || updatedProject != nil || updatedProfile != nil || invariantDiff != nil {
			a.mu.Lock()
			if c, ok := a.chats[chatID]; ok {
				if updatedTask != nil {
					agentReply.Task = c.Task
				}
				agentReply.CumulativeTotalTokens = c.CumulativeTotalTokens
				agentReply.CumulativeCostUsd = c.CumulativeCostUsd
				if invariantDiff != nil {
					c.setLastMessageInvariantDiff(branchID, *invariantDiff)
					agentReply.InvariantDiff = invariantDiff
					if err := a.store.Save(c); err != nil {
						log.Printf("agent: failed to persist chat %s after invariants update: %v", c.ID, err)
					}
				}
			}
			// KnownStack/Notes and Invariants can both have just mutated the
			// same Project concurrently (see the goroutines above) — re-read
			// it fresh here rather than trusting whichever call's own
			// returned copy, or the reply could show only one of the two
			// changes depending on which goroutine happened to finish last.
			if (updatedProject != nil || invariantDiff != nil) && projectID != "" {
				if p, ok := a.projects[projectID]; ok {
					agentReply.Project = projectCopy(p)
				}
			}
			if updatedProfile != nil {
				agentReply.Profile = updatedProfile
			}
			a.mu.Unlock()
		}
	}
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

// buildAgentReplyLocked assembles an AgentReply from chat's current state,
// describing the branch this particular turn happened on (branchID) rather
// than whichever branch happens to be active right now — those can differ
// if SetActiveBranch ran while this turn's LLM call was in flight. Callers
// must hold a.mu. Shared by PostMessage's success path and finishGracefulTurn
// so both return the exact same shape.
func (a *Agent) buildAgentReplyLocked(chat *Chat, branchID, reply string, usage *TokenUsage, userSentAt, assistantSentAt time.Time) *AgentReply {
	isCoordinator := false
	var fanOut []FanOutStatus
	if chat.LabID != "" {
		if lab, ok := a.labs[chat.LabID]; ok {
			isCoordinator = lab.CoordinatorChatID == chat.ID
		}
		if isCoordinator {
			fanOut = a.fanOutLocked(chat.LabID)
		}
	}
	var project *Project
	if chat.ProjectID != "" {
		project = a.projects[chat.ProjectID]
	}
	return &AgentReply{
		Reply:                     reply,
		Estimate:                  chat.branchEstimate(branchID),
		Title:                     chat.Title,
		Usage:                     usage,
		UserMessageCreatedAt:      userSentAt,
		AssistantMessageCreatedAt: assistantSentAt,
		LastContextTokens:         chat.branchLastContextTokens(branchID),
		CumulativeTotalTokens:     chat.CumulativeTotalTokens,
		CumulativeCostUsd:         chat.CumulativeCostUsd,
		ContextTokenLimit:         a.contextTokenLimit,
		ContextStrategy:           chat.ContextStrategy,
		HistoryKeepLastN:          a.historyKeepLastN,
		SummarizedMessageCount:    chat.SummarizedThrough,
		RawMessageCount:           len(chat.Messages) - chat.SummarizedThrough,
		Facts:                     chat.Facts,
		Branches:                  branchSummaries(chat),
		ActiveBranchID:            branchID,
		LabID:                     chat.LabID,
		IsLabCoordinator:          isCoordinator,
		FanOut:                    fanOut,
		ProjectID:                 chat.ProjectID,
		Project:                   project,
		Task:                      chat.Task,
		Profile:                   a.profile,
		TaskState:                 computeTaskState(chat),
	}
}

// buildMemorySystemMessages renders day 11's working-memory (task) and
// long-term-memory (project) layers, plus day 12's global profile, as system
// messages — a parallel injection to buildContextMessages, not part of its
// strategy switch: that switch is about how history gets windowed/
// compressed, this is a separate, always-on layer independent of
// ContextStrategy. profile is nil when the caller wants it withheld (lab
// chats — see PostMessage).
func buildMemorySystemMessages(task *TaskMemory, project *Project, profile *UserProfile) []chatMessage {
	var messages []chatMessage
	if profile != nil && (profile.Name != "" || len(profile.Stack) > 0 || profile.Style != "" || profile.Format != "" || len(profile.Constraints) > 0) {
		if encoded, err := json.Marshal(profile); err == nil {
			messages = append(messages, chatMessage{
				Role: "system",
				Content: "Профиль пользователя (JSON; имя, стиль/формат/ограничения — соблюдай их в каждом ответе, " +
					"обращайся по имени, если оно задано): " + string(encoded),
			})
		}
	}
	if task != nil && (task.Goal != "" || len(task.Constraints) > 0 || len(task.ClarifyingAnswers) > 0) {
		if encoded, err := json.Marshal(task); err == nil {
			messages = append(messages, chatMessage{
				Role: "system",
				Content: "Рабочая память текущей задачи (JSON; цель, принятые ограничения, " +
					"уже полученные ответы — используй как контекст, не переспрашивай то, что уже есть): " + string(encoded),
			})
		}
	}
	if project != nil && (len(project.KnownStack) > 0 || len(project.Notes) > 0) {
		payload := struct {
			Name       string   `json:"name"`
			KnownStack []string `json:"known_stack,omitempty"`
			Notes      []string `json:"notes,omitempty"`
		}{Name: project.Name, KnownStack: project.KnownStack, Notes: project.Notes}
		if encoded, err := json.Marshal(payload); err == nil {
			messages = append(messages, chatMessage{
				Role: "system",
				Content: "Долговременная память проекта «" + project.Name + "» (JSON; известна из других чатов этого же проекта — " +
					"используй как контекст, не переспрашивай то, что уже есть). Это СПРАВОЧНАЯ память, а не жёсткое " +
					"ограничение — сама по себе она не основание для отказа в предложении решения; если ниже отдельным " +
					"сообщением даны жёсткие инварианты проекта, для отказов руководствуйся ИМЕННО ими, а не этими заметками: " +
					string(encoded),
			})
		}
	}
	if project != nil && len(project.Invariants) > 0 {
		if encoded, err := json.Marshal(project.Invariants); err == nil {
			messages = append(messages, chatMessage{
				Role: "system",
				Content: "Жёсткие инварианты проекта «" + project.Name + "» (JSON-массив) — правила, которые НЕЛЬЗЯ нарушать " +
					"НИ ПРИ КАКИХ ОБСТОЯТЕЛЬСТВАХ, даже если пользователь прямо просит: " + string(encoded) + ". " +
					"Перед тем как предложить решение или оценку, проверь его на соответствие каждому инварианту. " +
					"Если запрос пользователя противоречит одному или нескольким — НЕ предлагай это решение и не включай " +
					"его в оценку; вместо этого в \"reply\" явно откажись, назови нарушенный инвариант дословно, объясни " +
					"противоречие и предложи альтернативу, если она есть. Перечисли в \"invariant_conflict\" точный текст " +
					"каждого нарушенного инварианта (пустой массив, если конфликта нет). ЭТОТ СПИСОК — ЕДИНСТВЕННОЕ " +
					"основание для отказа: если что-то раньше обсуждалось в переписке или упомянуто в долговременной " +
					"памяти проекта как решение/ограничение, но не входит в список выше — оно БОЛЬШЕ НЕ действует, " +
					"отказывать на этом основании нельзя.",
			})
		}
	}
	return messages
}

// contextFullReplyText is shown when the dialog's history genuinely leaves
// no usable room left — the two cases where the LLM was never even called
// (the pre-call guard) or was rejected outright by the upstream API.
func contextFullReplyText(tokens, limit int) string {
	return fmt.Sprintf(
		"⚠️ Достигнут лимит контекста диалога: %d / %d токенов. "+
			"Продолжить этот диалог нельзя — начните новый чат.",
		tokens, limit,
	)
}

// truncatedReplyText is shown when the history itself still had room, but
// this one turn's reply needed more than what remained and got cut off by
// the model provider (finish_reason "length") into something unusable.
func truncatedReplyText(budget int) string {
	return fmt.Sprintf(
		"⚠️ Ответ модели получился длиннее допустимого бюджета для одного "+
			"хода (%d токенов) и был обрезан. Попробуйте задать более "+
			"короткий или простой вопрос, либо начните новый чат.",
		budget,
	)
}

// finishGracefulTurn appends the user's message and a synthetic reply to
// chat, persists it, and returns the same AgentReply shape a normal turn
// would — so the frontend needs no special-casing for any of the paths that
// call it. reply is built by the caller (contextFullReplyText or
// truncatedReplyText) so each failure mode gets accurate wording.
//
// usage is nil when the LLM was never actually called (the pre-call guard,
// or an outright upstream rejection). When a call DID happen but its content
// came out unusable, pass its real usage: the request genuinely happened and
// cost real tokens, so the chat's running totals must reflect it. branchID is
// the branch this turn was snapshotted from (see PostMessage) — the same
// captured-not-re-read value buildAgentReplyLocked's own doc comment
// explains.
func (a *Agent) finishGracefulTurn(ctx context.Context, chat *Chat, branchID, userMessage, reply string, userSentAt time.Time, usage *TokenUsage) (*AgentReply, error) {
	assistantSentAt := time.Now()

	a.mu.Lock()

	chat.appendToBranch(branchID,
		AgentMessage{Role: "user", Content: userMessage, CreatedAt: userSentAt, Usage: usage},
		AgentMessage{Role: "assistant", Content: reply, CreatedAt: assistantSentAt, Usage: usage},
	)
	// A graceful turn never touches the estimate, so the stage can only have
	// moved by message count alone (e.g. intake -> clarifying on the very
	// first message) — still worth stamping for the same reason every real
	// turn is.
	chat.setLastMessageTaskState(branchID, computeTaskState(chat))
	if chat.Title == "Новый чат" {
		chat.Title = chatTitleFrom(userMessage)
	}
	if usage != nil {
		chat.setBranchLastContextTokens(branchID, usage.TotalTokens)
		chat.addUsage(usage)
	}
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s: %v", chat.ID, err)
	}

	chatID := chat.ID
	agentReply := a.buildAgentReplyLocked(chat, branchID, reply, usage, userSentAt, assistantSentAt)
	a.mu.Unlock()

	// A graceful turn (context full, or a truncated reply) never touches
	// facts, task memory, or project memory — there's no real assistant
	// content to extract any of them from — but history has still grown, so
	// a rolling_summary chat may still be due for a fold.
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

// parseAgentTurn strips optional code fences and unmarshals the model's
// {"reply", "estimate"} envelope, validating the estimate against the same
// schema the day-1 estimate endpoint enforces whenever one is present.
func parseAgentTurn(raw string) (*agentTurn, error) {
	cleaned := stripCodeFences(raw)

	var turn agentTurn
	if err := json.Unmarshal([]byte(cleaned), &turn); err != nil {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil, fmt.Errorf("%w: model did not return valid JSON: %v", ErrInvalidOutput, err)
		}
		// cleaned starting with "{" means the model was clearly attempting
		// the JSON envelope and it came out broken (most often: cut off
		// mid-object by a max_tokens budget) — showing that raw fragment as
		// if it were the chat reply is worse than an error.
		if strings.HasPrefix(cleaned, "{") {
			return nil, fmt.Errorf("%w: model's JSON response was malformed or cut off: %v", ErrInvalidOutput, err)
		}
		// Otherwise this is a plain conversational follow-up where the model
		// dropped the JSON envelope entirely and just answered in prose.
		// Treat that prose as the reply instead of failing the turn.
		log.Printf("agent: model dropped the JSON envelope, falling back to its raw text as the reply")
		return &agentTurn{Reply: trimmed}, nil
	}
	if strings.TrimSpace(turn.Reply) == "" {
		return nil, fmt.Errorf("%w: reply must not be empty", ErrInvalidOutput)
	}
	if turn.Estimate != nil {
		if err := turn.Estimate.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
		}
		if adjusted := turn.Estimate.ReconcileWithSubtasks(); adjusted {
			log.Printf("agent: model's total estimate did not match the sum of its subtasks, corrected to %.1f-%.1fh",
				turn.Estimate.EstimatedHoursMin, turn.Estimate.EstimatedHoursMax)
		}
	}

	return &turn, nil
}
