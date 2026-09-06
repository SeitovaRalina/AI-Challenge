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

## Planned data sources

- GitHub activity
- AI coding-agent sessions
- Yandex Calendar meetings
- manual tasks
- Solidtime
- potentially desktop activity collected by a local client

## Product areas (target state)

1. **Timesheet**
   - reconstruct work sessions
   - detect potentially untracked work
   - let the user edit, merge, split, reject, or accept suggestions
   - eventually sync approved entries to Solidtime

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

Until real historical user data exists, any estimate is a **preliminary, generic AI
estimate** — never framed as personalized ("based on your previous work", "you
usually need..."). Personalization is introduced later, once historical data and
retrieval (RAG) are in place.

## Delivery model

The project is built incrementally across a seven-week AI Challenge. Each week adds
one real capability to the pipeline above — it is not a course-topic showcase:

1. LLM fundamentals — generic task → preliminary AI estimate (current milestone)
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

Infrastructure such as PostgreSQL, Redis, Docker, auth, MCP, RAG, background
workers, desktop tracking, and the Solidtime/GitHub/calendar integrations are
introduced only in the later weeks listed above — not before they're needed.

## Current milestone

See [`../task.md`](../task.md) for the exact Day 1 scope, acceptance criteria, and
constraints (Software Task → AI Preliminary Estimate).
