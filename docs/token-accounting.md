# Token accounting & context-window simulation (Day 8)

This document explains, for a reviewer who did not write the code, how token
counts are produced, how the app enforces an artificial context-window limit,
why the displayed numbers can jump or even shrink between turns, and what
each error message means. It matches the implementation in `backend/agent.go`
and `backend/llm.go` as of the Day 8 branch.

## 1. Where the numbers come from

There is no local tokenizer anywhere in this codebase — token counts are
never estimated client-side. Every number shown in the UI (per-message
captions, the footer, the `/tokens` popup) comes straight from the `usage`
object the LiteLLM gateway returns on every `/v1/chat/completions` call:

```go
type chatCompletionUsage struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
    Cost             *float64
}
```

(`backend/llm.go`, `tokenUsageFrom` in `backend/agent.go` converts this into
the app's own `TokenUsage` type.) `prompt_tokens` is what the model's own
tokenizer measured for everything sent to it on that call; `completion_tokens`
is the length of what it generated back.

## 2. What "context" means in this simulation

Real LLM APIs are stateless: the server holds no memory between requests,
so the client must resend the entire conversation history on every call.
`prompt_tokens` of any given call is therefore already a measurement of the
whole history up to that point, not just the newest message.

`Chat.LastContextTokens` (`backend/agent.go`) is this app's tracked
approximation of "how full the simulated context window currently is". After
every call that produces usable output, it is set to:

```go
chat.LastContextTokens = usage.TotalTokens // prompt_tokens + completion_tokens
```

Using `prompt_tokens` alone was tried first and rejected: it excludes the
reply the model *just wrote*, even though that reply is appended to history
and will itself be sent back as part of the prompt on the next turn. That
undercount produced misleading messages like "0 / 1200 tokens used" on a
chat's very first turn (a real call had happened and used real tokens, the
snapshot just hadn't caught up yet). Using `total_tokens` fixes that
specific undercount — see §5 for the tradeoff it introduces instead.

## 3. Enforcing the limit — two layers

`CHAT_CONTEXT_TOKEN_LIMIT` (env var, default 128000, lowered for demos, e.g.
900–6000) is compared against `Chat.LastContextTokens` before every call,
in `Agent.PostMessage`:

```go
const minCompletionBudget = 64
remainingBudget := contextTokenLimit - lastContextTokens
if remainingBudget < minCompletionBudget {
    // skip the call entirely — dialog full
}
maxTokens := remainingBudget // capped real max_tokens sent upstream
```

**Layer 1 — pre-call guard.** If less than `minCompletionBudget` (64) tokens
of room remain, the LLM is never called at all. This mirrors a real API's
upfront `context_length_exceeded` rejection, at zero cost.

**Layer 2 — real `max_tokens` capping.** Otherwise, the real HTTP request to
LiteLLM is sent with `max_tokens = remainingBudget`. This is what makes the
overflow simulation realistic: the *actual* upstream model enforces this
budget itself, the same way a real context window bounds a single turn's
completion. If the model needs more room than that, the real API truncates
it (`finish_reason: "length"`) — we are not synthesizing this behavior, we
are triggering the genuine mechanism real providers use.

`isContextOverflow` (`backend/llm.go`) additionally recognizes a real
upstream `context_length_exceeded`-style rejection (by status code + body
text), classified as `ErrContextOverflow`, in case the *real* model's own
(much larger) context window is ever hit independently of our simulated one.

## 4. The two error messages, and when each fires

Two structurally different situations exist, and — after a fix during this
branch — each gets its own honest message. Reusing one message for both was
the original bug: it produced nonsense like "882 / 1200 tokens — limit
reached" when 882 is nowhere near 1200.

| Situation | Trigger | Message | LLM called? |
|---|---|---|---|
| History itself leaves no room | `remainingBudget < minCompletionBudget` (pre-call guard), or a real `ErrContextOverflow` rejection | `contextFullReplyText`: *"⚠️ Достигнут лимит контекста диалога: N / LIMIT токенов. Продолжить этот диалог нельзя — начните новый чат."* | No (guard) / Yes but rejected (real overflow) |
| History had room, but this one reply didn't fit its per-turn budget | Real call returns `finish_reason == "length"` and the (truncated) content isn't usable — either empty, or JSON that starts with `{` but is cut off mid-object | `truncatedReplyText`: *"⚠️ Ответ модели получился длиннее допустимого бюджета для одного хода (N токенов) и был обрезан. Попробуйте задать более короткий или простой вопрос, либо начните новый чат."* | Yes — call happened, cost real tokens |

The second row exists because this routed model is a reasoning model: it can
spend its entire truncated budget on hidden reasoning tokens and return
**empty visible content**, or get cut off mid-JSON. `parseAgentTurn`
(`backend/agent.go`) distinguishes this from a model that simply dropped the
JSON envelope and answered in plain prose (still a valid Day-7 fallback) by
checking whether the cleaned output starts with `{`: if it does and still
fails to parse, it's a broken JSON attempt, not conversational text, and
must not be shown to the user verbatim.

In both graceful-failure cases, if a real call happened and returned usage
(second row), that usage is still recorded into `chat.LastContextTokens` and
the cumulative totals — the call cost real tokens even though its content
was unusable, and the running totals must reflect that instead of staying
frozen at a stale pre-call value.

## 5. Why the displayed numbers can jump or shrink between turns

This is expected behavior given how the app stores history, not a bug — but
it is worth explaining because it looks surprising in a demo.

**What actually gets kept in history is much smaller than what a call
costs.** `chat.Messages` stores only the `"reply"` string extracted from the
model's JSON envelope — never the full envelope, and never the `estimate` /
`subtasks` / `risks` / `assumptions` payload, which can be by far the
largest part of `completion_tokens`. The current estimate is instead resent
on every turn as its own compact system message, re-serialized from the
stored `EstimateResponse` struct:

```go
if currentEstimate != nil {
    messages = append(messages, chatMessage{
        Role: "system",
        Content: "Текущая актуальная оценка задачи (JSON, ...): " + json-encoded estimate,
    })
}
```

So a verbose turn (say, `completion_tokens = 2609` because the model wrote a
long `subtasks` breakdown) contributes only a short sentence plus a compact
JSON blob to every future prompt — nowhere near 2609 tokens' worth. A
truncated turn contributes even less: the raw (unusable, possibly huge)
truncated output is discarded entirely and replaced with the short synthetic
`truncatedReplyText` notice before being stored.

**Concrete example observed on this branch** (limit 4000):

```
turn 1: prompt=806  completion=2609 total=3415   -> LastContextTokens = 3415
turn 2: truncated (budget = 4000-3415 = 585)      -> LastContextTokens updated from that call's own usage
turn 3: prompt=1151 completion=1884 total=3035   -> LastContextTokens = 3035
```

Turn 3's `prompt_tokens = 1151` is the model's own, real, freshly-measured
count of the *entire* history up to that point (system prompt + compact
estimate + turns 1–2 as actually stored) — and it is much smaller than
turn 1's `total = 3415`, because turn 1's `total` counted the model's full
raw generation cost, most of which was never kept. This is why the
displayed "context" figure is best understood as **an upper-bound proxy for
the guard**, not a literal live measurement of history size: it can safely
overestimate (which only makes the app *more* conservative than necessary)
but should never be read as "exactly how many tokens are in the history
right now" — the model's own next `prompt_tokens` is the only ground truth
for that.

## 6. Known limitation / open design tension

There is no way to know a call's real `prompt_tokens` before making it
(no local tokenizer, by design — see the Day 8 planning notes: LiteLLM's own
`usage` field is the single source of truth). This means:

- Using `prompt_tokens` alone as the tracked value undercounts (excludes the
  reply just written, which will occupy real space next turn) — this was
  the pre-fix bug.
- Using `prompt_tokens + completion_tokens` (current implementation)
  overcounts relative to what is actually resent, because most of the raw
  completion is never stored back into history — this is what causes the
  guard to sometimes trigger `truncatedReplyText` earlier than strictly
  necessary, and causes the displayed number to occasionally drop turn over
  turn as verbose completions get compressed into short stored history.

Both directions are safe (neither can silently let the real upstream model
run over its real context window), but neither is a perfectly accurate
live counter. This is an accepted tradeoff for a demo-scale simulation
without a local tokenizer, not an unnoticed bug.

## 7. Configuration

`CHAT_CONTEXT_TOKEN_LIMIT` (`backend/.env`, see `backend/.env.example`):
defaults to 128000 (`defaultContextTokenLimit` in `backend/main.go`); lower
it (e.g. 900–6000) to reliably reproduce both failure modes within a short,
demoable conversation. `0` disables the guard entirely and always calls
through to the LLM with `max_tokens` unset.

The unrelated HTTP handler timeout for the chat-message endpoint
(`handler.go`, `context.WithTimeout(r.Context(), 60*time.Second)`) is a
separate, pre-existing constraint from before Day 8: at very large
`max_tokens` budgets (≈10000+, observed on `llm.effective.land`), the
upstream model can take longer than 60s to respond, producing a `502
сервис LLM сейчас недоступен` unrelated to the context-limit logic above.
