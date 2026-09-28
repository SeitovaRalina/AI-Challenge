# AI Advent Challenge #9

Repository for **AI Advent Challenge #9** by Alexey Gladkov.

Challenge details: https://mobiledeveloper.tech/ai_advent_9

## Repository structure

- `main` — stable line, accumulates the product as challenge days are completed.
- Each day gets its own branch `feature/wNN-dDD-slug` (e.g. `feature/w01-d02-structured-output`), merged into `main` via a Pull Request.
- The day's assignment text lives in `days/wNN-dDD-slug.md` (in Russian, verbatim from the challenge).
- The PR description records what was specifically implemented for that day's assignment.

---

# AI Work Intelligence Assistant

Software Task → AI Preliminary Estimate.

The user describes a development task in a chat interface; a Go-side `Agent`
entity (`backend/agent.go`) owns the conversation, calls the company LiteLLM
gateway, validates the model's JSON response against the app schema, and
returns a preliminary estimate (summary, category, complexity, hour range,
risks, assumptions, and — for large enough tasks — a subtask breakdown with
its own per-subtask hour range) plus a conversational reply. Follow-up
messages in the same chat reuse its full history plus the exact current
estimate, so the user can refine the estimate, or ask "how long will subtask
X take", and get an answer grounded in the real numbers rather than a guess.
Several chats can be open at once, each with its own isolated history.

Every chat is persisted to disk as it's used (one JSON file per chat under
`backend/data/sessions/`, path configurable via `CHAT_DATA_DIR`) and reloaded
on startup, so restarting the backend does not lose any conversation — see
[`days/w02-d07-context-persistence.md`](days/w02-d07-context-persistence.md).
Labs, projects, and the global profile live in sibling directories derived
from `CHAT_DATA_DIR` — see "Multiple profiles" below for running a fully
separate instance for a different person.

Token/cost usage is read directly from the LiteLLM gateway's `usage` field
(no local tokenizer), and an artificial `CHAT_CONTEXT_TOKEN_LIMIT` bounds
each chat's simulated context window with a real, model-enforced `max_tokens`
cap — see [`days/w02-d08-tokens.md`](days/w02-d08-tokens.md) and, for the
implementation detail and known approximation tradeoffs,
[`docs/token-accounting.md`](docs/token-accounting.md).

This is a **preliminary AI estimate**, grounded in an explicit memory model —
short-term (this chat), working (this chat's own task state), and long-term
(a project's known stack/notes, and a single global user profile) — see
[`days/w03-d11-agent-memory-model.md`](days/w03-d11-agent-memory-model.md) and
[`days/w03-d12-personalization.md`](days/w03-d12-personalization.md). See
[`docs/concept.md`](docs/concept.md) for the overall product vision, and
[`days/`](days/) for each day's exact assignment scope as the product grows.

Week 4 connects real work-activity sources over MCP (Model Context
Protocol): the product's own GitHub, Calendar (CalDAV) and Worklog MCP
servers, plus a background collector that keeps a local history of commits,
PRs, reviews and meetings. The chat agent picks the right server and tool
per question — the local history by default, a live call only when the
user explicitly asks for fresh data — and can chain several calls in one
turn for a longer question ("what did I do yesterday", "this week vs
last"). The «Источники» screen shows each server's connection and tools;
«Активность» shows the collector's own status and a raw event feed.

On top of that, an automatic pipeline turns events into work and meeting
sessions (a meeting always wins any time it shares with work, so nothing
is double-counted), powering the **«Аналитика»** screen: KPIs, hours by
project/day, a commit-type breakdown, a weekday×hour heatmap, an 8-week
trend, a Gantt-style day timeline, and a weekly summary comparing this
week to last in the user's own tone — facts only, never a productivity
score. See [`days/`](days/) (`w04-d16-mcp-connection.md` through
`w04-d20-mcp-orchestration.md`) for each day's exact scope.

The original day-1 through day-5 one-shot demos (structured output, reasoning
strategies, temperature, model versions) are still available from the
sidebar, collapsed under "День 1–5 (демо)" — the chat agent is now the
primary interface.

Note: the product UI itself is in Russian (target audience), while this
documentation is in English.

## Stack

- Backend: Go (standard library `net/http`, no framework)
- Frontend: React + TypeScript + Vite, Tailwind CSS v4, shadcn/ui
- LLM: company LiteLLM gateway (`https://llm.effective.land`),
  OpenAI-compatible `/v1/chat/completions` endpoint

## Requirements

- Go 1.22+
- Node.js 20+

## Running the project

### Backend

```
cd backend
cp .env.example .env
```

Fill in `backend/.env`:

```
LITELLM_API_KEY=your-key
LITELLM_MODEL=model-name
```

Run:

```
go run .
```

The API listens on `http://localhost:8080` (override via `PORT`). The LiteLLM
key is used server-side only and never reaches the browser.

For the «Источники» screen, also set `GITHUB_TOKEN` in `backend/.env` — a
fine-grained GitHub personal access token with read-only Metadata, Contents,
Pull requests and Issues permissions. It is sent only to the GitHub MCP
server (with `X-MCP-Readonly: true`, so only read-only tools are exposed)
and is never returned to the browser. `GITHUB_MCP_URL` overrides the default
`https://api.githubcopilot.com/mcp/` endpoint.

The same token powers the product's own GitHub Activity MCP server
(day 17). The backend starts it as a stdio subprocess with
`go run ./cmd/mcp-github` — so run the backend from `backend/` — or with
`MCP_GITHUB_COMMAND` pointing at a prebuilt binary. It reads every repository
the token can see, or only those listed in `GITHUB_REPOS` (comma-separated
`owner/repo`); for "every repository", give the fine-grained token
**Repository access → All repositories**.

The Worklog MCP server (day 18) needs no separate setup — it starts the same
way (`go run ./cmd/mcp-worklog` by default, `MCP_WORKLOG_COMMAND` to point at
a binary) and stores its SQLite file at `WORKLOG_DB`, defaulting next to the
chat sessions. The background collector that feeds it runs automatically
once `GITHUB_TOKEN` is set; `COLLECT_INTERVAL` changes how often (`0`/`off`
to disable the schedule and only ever collect via the «Активность» screen's
"Собрать сейчас").

The Calendar MCP server (day 20) is optional — without `CALDAV_USERNAME`
and `CALDAV_APP_PASSWORD` in `backend/.env` it simply isn't started, and
the collector's calendar step is skipped with a warning; GitHub-only
collection is unaffected. To enable it: `CALDAV_USERNAME` is usually your
email, `CALDAV_APP_PASSWORD` an app-specific password (Yandex:
id.yandex.ru → Безопасность → Пароли приложений → CalDAV), never your
account password. `CALDAV_URL` defaults to Yandex's endpoint
(`https://caldav.yandex.ru`); `CALDAV_CALENDARS` optionally narrows
collection to specific calendars by name (comma-separated), defaulting to
every calendar on the account. Same `go run ./cmd/mcp-calendar` /
`MCP_CALENDAR_COMMAND` pattern as the other two servers.

### Frontend

```
cd frontend
npm install
npm run dev
```

Open `http://localhost:5173`. In dev mode, Vite proxies `/api/*` to
`http://localhost:8080`, so both servers need to be running.

### Multiple profiles

The app is a **personal** assistant, not a multi-tenant one — see
[`docs/concept.md`](docs/concept.md). Its memory model (chats, projects,
the global profile) belongs to whoever is running that instance, so there is
deliberately no account system or in-app profile switcher: the personalization
day 12 adds (name, tone, response format, constraints) only makes sense
addressed to one specific person at a time, and letting one running instance
juggle several people's data would work against the whole point of days
11-12 — an agent that actually knows the person it's talking to.

"Multiple profiles" instead means multiple independent **processes**, each
with its own data tree, picked at startup via `AGENT_USER`:

```
cd backend
AGENT_USER=bob PORT=8081 go run .
```

(PowerShell: `$env:AGENT_USER="bob"; $env:PORT="8081"; go run .`)

This namespaces that whole instance's chats/labs/projects/profile.json under
`backend/data/bob/`, completely isolated from the default instance's
`backend/data/`. Explicit `CHAT_DATA_DIR` still overrides `AGENT_USER` if you
need finer-grained control than one directory per name.

To talk to that second backend from a browser, point a second frontend at
its port — Vite's dev proxy target is hardcoded in `vite.config.ts`, so
either edit it temporarily or run a second `vite` process from its own
config with `server.port`/`server.proxy['/api'].target` set to match.

Filling in that instance's profile works exactly like the default one — the
sidebar's profile button, or the empty-chat "Начать интервью с ассистентом"
prompt — it's just a different person's data underneath.

## Testing the backend standalone

```
curl -s http://localhost:8080/api/estimate \
  -X POST -H "Content-Type: application/json" \
  -d '{"task":"Upgrade a legacy Flutter app to the latest Flutter version, update dependencies, fix iOS and Android build issues, and prepare new builds."}'
```

Chat agent:

```
chat_id=$(curl -s -X POST http://localhost:8080/api/agent/chats | jq -r .id)

curl -s -X POST http://localhost:8080/api/agent/chats/$chat_id/messages \
  -H "Content-Type: application/json" \
  -d '{"message":"Upgrade a legacy Flutter app to the latest Flutter version."}'
```

MCP sources (day 16) — list the configured servers, then connect to one
(initialize + tools/list):

```
curl -s http://localhost:8080/api/mcp/servers

curl -s -X POST http://localhost:8080/api/mcp/servers/github/connect
```

The same connection from the terminal, without starting the backend:

```
cd backend
go run ./cmd/mcp-tools            # server info + tools table
go run ./cmd/mcp-tools -params    # plus every tool's input parameters
```

The own GitHub Activity MCP server (day 17) is also listed there, with id
`github-activity`:

```
curl -s -X POST http://localhost:8080/api/mcp/servers/github-activity/connect
```

Ask the agent about your own activity — the reply carries the tool calls it
made in `tool_calls`:

```
curl -s -X POST http://localhost:8080/api/agent/chats/$chat_id/messages \
  -H "Content-Type: application/json" \
  -d '{"message":"Какие PR я смёржила на этой неделе?"}'
```

The same turn with live progress, as server-sent events (`routing`,
`tool_call_started`, `tool_call_finished`, `answering`, `answer`, then
`done` with the same reply, or `error`):

```
curl -sN -X POST http://localhost:8080/api/agent/chats/$chat_id/messages/stream \
  -H "Content-Type: application/json" \
  -d '{"message":"Что я делала вчера?"}'
```

The background collector (day 18) — status (last/next run, run log), a
manual run, and what it has stored so far:

```
curl -s http://localhost:8080/api/activity/status
curl -s -X POST http://localhost:8080/api/activity/collect

curl -s "http://localhost:8080/api/activity/digest?period=7d"
curl -s "http://localhost:8080/api/activity/events?period=7d&repo=AI-Challenge"
```

These are the same Worklog MCP calls (`get_sync_state`, `get_activity_digest`,
`list_events`) the chat agent makes when asked about past work — a question
now prefers this local data over a live `github.get_activity` call:

```
curl -s -X POST http://localhost:8080/api/agent/chats/$chat_id/messages \
  -H "Content-Type: application/json" \
  -d '{"message":"Сколько всего коммитов у меня за последние 30 дней?"}'
```

The session pipeline and Analytics screen (day 19) — sessions are rebuilt
automatically at the end of every collector run above; this only reads the
result:

```
curl -s "http://localhost:8080/api/analytics?period=30d"

curl -s http://localhost:8080/api/analytics/repos
curl -s -X PATCH http://localhost:8080/api/analytics/repos/owner/repo \
  -H "Content-Type: application/json" -d '{"project":"AI Challenge"}'
```
