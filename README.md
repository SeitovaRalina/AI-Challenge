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
