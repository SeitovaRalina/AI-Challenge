# AGENTS.md

## Project concept

See [`docs/concept.md`](docs/concept.md) for the overall product vision, the
target pipeline (work activity → timesheet → analytics → estimates → forecast),
and the long-term technical direction.

## Current milestone

The active scope, acceptance criteria, and hard constraints for the current
iteration are defined in [`task.md`](task.md). Do not implement beyond that
scope without checking `docs/concept.md` for whether it fits the target
architecture.

## Rules for agents working in this repo

- Never hardcode or commit secrets (`LITELLM_API_KEY`, etc.). Use env vars.
- Keep changes scoped to the current milestone in `task.md`; later-phase
  features (RAG, MCP, auth, background workers, integrations) belong to later
  weeks of the roadmap in `docs/concept.md`.
- Repository language: English (code, comments, commit messages, docs).
