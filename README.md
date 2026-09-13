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
risks, assumptions) plus a conversational reply. Follow-up messages in the
same chat reuse its full history, so the user can refine the estimate by
adding details or constraints instead of resubmitting a whole new task.
Several chats can be open at once, each with its own isolated history
(in-memory only for now — see [`days/w02-d06-agent.md`](days/w02-d06-agent.md)).

This is a **preliminary, generic AI estimate**. There is no user history yet, so
nothing here is personalized. See [`docs/concept.md`](docs/concept.md) for the
overall product vision, and [`days/`](days/) for each day's exact assignment
scope as the product grows.

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
