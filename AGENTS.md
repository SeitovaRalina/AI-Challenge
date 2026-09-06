# AGENTS.md

## Project concept

See [`docs/concept.md`](docs/concept.md) for the overall product vision, the
target pipeline (work activity → timesheet → analytics → estimates → forecast),
and the long-term technical direction.

## Current milestone

Each day of the challenge lives on its own branch, `feature/wNN-dDD-slug`
(e.g. `feature/w01-d02-structured-output`), merged into `main` via a Pull
Request. The active scope, acceptance criteria, and hard constraints for the
day being worked on are defined in that branch's `days/wNN-dDD-slug.md` file.
Do not implement beyond that day's scope without checking `docs/concept.md`
for whether it fits the target architecture.

## Rules for agents working in this repo

- Never hardcode or commit secrets (`LITELLM_API_KEY`, etc.). Use env vars.
- Keep changes scoped to the current day's file under `days/`; later-phase
  features (RAG, MCP, auth, background workers, integrations) belong to later
  weeks of the roadmap in `docs/concept.md`.
- Repository language: English (code, comments, commit messages, docs). The
  `days/*.md` assignment files and the product UI are Russian — that's
  content, not code, and stays as-is.
