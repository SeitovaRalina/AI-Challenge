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
- PR descriptions follow `.github/pull_request_template.md` (Day's
  assignment / What's implemented / Verified). Fill in that template rather
  than inventing a different structure per PR.

## Day workflow

This is the standard scope for every day, independent of that day's specific
topic — follow it whether or not it's spelled out again in the request:

1. Record that day's assignment verbatim in `days/wNN-dDD-slug.md` (see
   "Current milestone" above) before doing anything else.
2. Plan first: for any pasted "Day N" assignment, propose an implementation
   plan (architecture, scope, open questions) and get the user's explicit
   approval before writing code. Skip this step only for small, obvious
   follow-up fixes within an already-approved day's work.
3. Implement — backend and frontend, as the day requires.
4. Verify before calling anything done: backend `go build ./... && go vet
   ./...`; frontend `npx tsc -b`, `npm run build`, `npm run lint`. All must
   be clean — no new errors or warnings beyond what already existed before
   the change.
5. Test on an isolated instance: its own port and its own `CHAT_DATA_DIR` /
   data directory (e.g. under a scratch/temp dir), never the user's own
   running dev servers. Get the user's explicit permission before any
   browser-automation (claude-in-chrome) test — every time, even for a
   change that looks self-verifying.
6. Commit as you go, in small, logically-scoped commits with a descriptive
   body (why, not just what) — on any day branch, without asking first,
   until the user says to stop.

## Git & PR workflow

- Branches: `feature/wNN-dDD-slug`, one per day (see "Current milestone").
- Commit subject style: `type(wNN-dDD): summary` — `feat`, `fix`, `docs`,
  `refactor`, matching this repo's existing history.
- No manual line-wrapping inside a commit or PR bullet — one physical line
  per bullet.
- PRs follow `.github/pull_request_template.md` (see above).
- Merging into `main` (squash) happens only with the user's explicit
  go-ahead for that specific PR — never merge unprompted, even once CI/
  review looks clean.
- Never pass `--delete-branch` when merging — the day's branch stays after
  merge, on GitHub and locally.
- After a merge, switch the local checkout to `main` and pull, so the next
  day starts from an up-to-date base.
