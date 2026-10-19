# Product Concept — AI Work Intelligence Assistant

## What this is

A personal AI-powered work intelligence system for software developers. It helps a
developer understand where their working time actually goes, reconstruct timesheets
from real activity, analyze personal productivity patterns, and estimate future
software tasks based on their own historical performance.

It is **not** a generic AI chat application and **not** a generic task manager.

## Core pipeline

```
work activity
    ↓
structured work history
    ↓
timesheet reconstruction
    ↓
productivity analytics
    ↓
personal task estimation
    ↓
future workload forecasting
```

## Data sources

- GitHub activity, Yandex Calendar meetings and Solidtime entries are connected.
- AI coding-agent sessions, manual tasks and desktop activity are planned.

## Product areas (target state)

1. **Timesheet**
   - reconstruct work sessions
   - detect potentially untracked work
   - let the user edit, merge, split, reject, or accept suggestions
   - sync individually confirmed entries to Solidtime (implemented)
   - edit, merge and split session intervals (planned)

2. **Analytics**
   - time by project
   - time by activity type
   - focus sessions
   - idle periods
   - context switching
   - meeting load
   - productive hours
   - weekly/monthly trends

3. **Estimates**
   - accept a new software-development task
   - analyze its characteristics
   - retrieve similar historical tasks (later, via RAG)
   - estimate how long *this specific user* is likely to need
   - show uncertainty instead of pretending the estimate is exact

4. **Forecast**
   - planned workload
   - meetings
   - available capacity
   - predicted effort
   - risk of overload

## Important semantic rule

An estimate is called personalized only when retrieval finds comparable past
tasks. If comparable history is missing, the assistant says so and lowers its
confidence instead of inventing personal experience.

## Delivery model

The project is built incrementally across a seven-week AI Challenge. Each week adds
one real capability to the pipeline above — it is not a course-topic showcase:

1. LLM fundamentals — generic task → preliminary AI estimate
2. Agents and context
3. Agent/state optimization
4. MCP
5. RAG
6. Local AI
7. Pipeline and automation

Every week's implementation must remain a genuinely useful slice of the final
product. Features are not added solely to satisfy a course topic.

## Technical direction

- Backend: Go
- Frontend: React + TypeScript + Vite, Tailwind CSS, shadcn/ui where appropriate
- LLM access: company LiteLLM gateway (`https://llm.effective.land`,
  OpenAI-compatible endpoints), via `LITELLM_API_KEY` / `LITELLM_MODEL` env vars,
  never hardcoded, never exposed to the browser

The current product uses local file and SQLite storage, MCP integrations, RAG
over project documentation and historical estimates, and a background activity
collector. Its API has no authentication; a future VPS deployment must keep
the application private behind an SSH tunnel or equivalent access control.
PostgreSQL, Redis, Docker and desktop tracking are not part of the current
implementation.

## Current milestone

See [`../README.md`](../README.md) for the implemented workflow and
[`../days/`](../days/) for the original daily challenge assignments.
