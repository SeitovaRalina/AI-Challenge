# Day 28: local RAG and provider comparison

## Product path

The standalone **Similar tasks** screen uses the Week 5 persisted index. Ollama's `nomic-embed-text` embeds each question; cosine retrieval over the local index supplies the source chunks. The user selects `qwen2.5:3b` through Ollama or LiteLLM for RAG answers and control-question evaluation. Query rewriting and reranking, when enabled, use the selected provider. With Ollama selected, the whole RAG path runs locally and requires no LiteLLM credentials.

`POST /api/rag/compare/providers` is a controlled provider comparison: one local retrieval yields one fixed source set for both generators. Each generator gets the same prompt and chunks. The response separates retrieval time from generation time and includes each provider's answer, server-checked citations, or error. The local result survives a cloud failure. Existing query and evaluation API callers without a `provider` field keep their LiteLLM default; the UI defaults to Ollama.

## Verification

- `go build ./...`, `go vet ./...`, and the isolated `backend/tests` HTTP regression test passed.
- `npx tsc -b`, `npm run build`, and `npm run lint` passed. Lint printed existing warnings in unrelated/shared files; the build printed its existing large-chunk warning.
- The HTTP regression test used a synthetic one-chunk index and mock Ollama/LiteLLM servers. It checked one embedding request per comparison, identical source context, both citations verified, a local answer without cloud credentials, and a local control-question run.
- A separate isolated backend used three synthetic chunks and the installed `nomic-embed-text` and `qwen2.5:3b` models. No user chats or stored index were copied. The same three questions were then sent to the configured LiteLLM gateway after the sandbox proxy was bypassed for this test process.

| Question | Expected source | Local answer | Cloud answer | Retrieval | Local generation | Cloud generation |
| --- | --- | --- | --- | ---: | ---: | ---: |
| CSV export estimate | `csv` | 2 hours | 2 hours | 61 ms | 4,330 ms | 4,388 ms |
| Analytics filter risk | `filters` | Slow queries | Slow queries | 52 ms | 6,133 ms | 2,567 ms |
| Payment import estimate and risk | `payments` | 6 hours; duplicates | 6 hours; duplicates | 79 ms | 7,081 ms | 2,393 ms |

Both providers answered all three synthetic questions correctly and returned three of three verified citations. Retrieval averaged 64 ms; local generation averaged 5,848 ms; cloud generation averaged 3,116 ms. All three paired requests succeeded in the direct-network run. The sandbox proxy (`127.0.0.1:9`) blocked the initial cloud run; its errors appeared only in the cloud result while local answers still succeeded. These timings are observations from one short run, not a performance benchmark or evidence of quality on the user's full Week 5 corpus. The UI can inspect that corpus without rebuilding it.

The first local run exposed a model-output variation: Qwen copied a chunk heading such as `[csv] (session ...)` into `chunk_id`. Citation verification now extracts that exact ID and still validates the quoted text against the retrieved chunk. The repeated local run returned three verified citations.

## Main chat timeout observed during demo

With **History** enabled in a local main chat, the installed `qwen2.5:3b` runs on CPU. Ollama's local log showed a 2,796-token tool-routing prompt taking 88 seconds, followed by an answer request canceled around the chat's former 120-second deadline. The UI surfaced that cancellation as "LLM service unavailable" even though embeddings and the index were healthy. Local chat turns now get a 240-second deadline for routing and generation; cloud turns retain 120 seconds. This is a reliability allowance, not a speed improvement. The running backend must be restarted to pick up the Go change; persisted chats remain in their existing data directory.
