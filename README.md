# AI Advent Challenge #9

Repository for **AI Advent Challenge #9** by Alexey Gladkov.

Challenge details: https://mobiledeveloper.tech/ai_advent_9

## Repository structure

- `main` — stable line, accumulates the product as challenge days are completed.
- Each day gets its own branch `feature/wNN-dDD-slug` (e.g. `feature/w01-d02-structured-output`), merged into `main` via a Pull Request.
- The day's assignment text lives in `days/wNN-dDD-slug.md` (in Russian, verbatim from the challenge).
- The PR description records what was specifically implemented for that day's assignment.

---

# AI Work Intelligence Assistant — Week 1 Day 1 (LLM Fundamentals)

Software Task → AI Preliminary Estimate.

The user describes a development task in the web UI, the React frontend sends it
to the Go backend, the backend calls the company LiteLLM gateway, validates the
model's JSON response against the app schema, and returns a structured
preliminary estimate (summary, category, complexity, hour range, risks,
assumptions).

This is a **preliminary, generic AI estimate**. There is no user history yet, so
nothing here is personalized. Overall product vision —
[`docs/concept.md`](docs/concept.md); this day's exact scope —
[`days/w01-d01-llm-api-request.md`](days/w01-d01-llm-api-request.md).

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

## Notes

- No persistence, no auth, no history — this stage is a single request/response
  cycle.
- Never commit `.env` files or hardcode the LiteLLM key.
