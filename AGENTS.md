# AGENTS.md

## Project concept

See [`docs/concept.md`](docs/concept.md) for the overall product vision, the
target pipeline (work activity → timesheet → analytics → estimates → forecast),
and the long-term technical direction.

## Current milestone

Each day of the challenge lives on its own branch, `feature/wNN-dDD-slug`
(e.g. `feature/w01-d02-structured-output`), merged into `main` via a Pull
Request. That branch's `days/wNN-dDD-slug.md` file holds the day's original,
short assignment text as given by the challenge — it is not a product spec.
The actual scope, acceptance criteria, and constraints for how that
assignment gets built into this specific product are a product decision
captured in the root `README.md` (and, for cross-cutting direction,
`docs/concept.md`). Do not implement beyond what those two describe without
checking `docs/concept.md` for whether it fits the target architecture.

## Rules for agents working in this repo

- Never hardcode or commit secrets (`LITELLM_API_KEY`, etc.). Use env vars.
- Keep changes scoped to the current day's milestone as described in
  `README.md`; later-phase features (RAG, MCP, auth, background workers,
  integrations) belong to later weeks of the roadmap in `docs/concept.md`.
- Documentation language: English. `README.md`, `AGENTS.md`, `docs/*`, code
  comments, commit messages, and PR descriptions are all written in English.
  The only exceptions are `days/*.md` (verbatim challenge assignment text)
  and the product UI/UX copy itself — both are Russian, by content
  requirement, not by omission.
