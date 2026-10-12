# Day 29: local chat optimization

## Scope

The target is the task-estimation chat using local `qwen2.5:3b` on the Windows
development PC. The existing LiteLLM chat path is unchanged. The local chat
still uses the Week 5 RAG index when History is enabled and still routes
activity questions through MCP tools when they are available.

## Changes

- Local answer temperature defaults to `0.1` (`OLLAMA_CHAT_TEMPERATURE`, valid range 0–2); the answer cap defaults to 1024 tokens (`OLLAMA_CHAT_MAX_TOKENS`, valid range 128–8192).
- The application budgets 4096 context tokens for local chat (`OLLAMA_CHAT_CONTEXT_WINDOW`, valid range 2048–32768). This does not change Ollama's actual `num_ctx`; configure that in an Ollama Modelfile and confirm the running value with `/perf` before increasing the app budget.
- The local answer request uses Ollama's `response_format: {"type":"json_object"}`. Its prompt also calls out exact JSON keys and forbids invented historical facts. Tool-routing requests remain outside JSON mode.
- A clear request to estimate or revise a task skips the separate model call that decides whether to use work-activity tools. Ambiguous questions and explicit activity/calendar questions still use the existing router.
- `/perf` reads the saved provider, model, answer latency, and tokens for this chat's last ten assistant turns. Ollama's `/api/ps` supplies loaded model size, VRAM allocation, quantization, and actual context length. The panel explicitly separates loaded size from host RAM usage.
- For an explicit question about the hours of a similar past task, a small local model sometimes invents a new estimate, omits the relevant citation, or describes a preliminary estimate as work actually completed. When retrieval contains a preliminary estimate with an hour range, the backend renders that narrow answer from the retrieved range, marks actual hours unknown, and verifies a citation against the source. Other questions keep the model's normal answer.

## Reproducing measurements

The local-only `demo/w06-d29-benchmark.py` starts its own backend on an unused
port with a new data directory. It sends the same three prompts through the
real chat API and samples Ollama process RSS and CPU with `psutil`. It never
loads or copies existing conversations. First build the binary from the
revision under test, then run:

```powershell
cd backend
go build -o ..\demo\w06-d29-optimized.exe .
cd ..
python demo/w06-d29-benchmark.py demo/w06-d29-optimized.exe demo/w06-d29-optimized.json
```

CPU is the summed Ollama process percentage relative to one logical core;
RSS is the sum of the sampled Ollama processes, including the model runner.
The script does not measure response quality automatically; inspect the
recorded replies, estimates, and HTTP status for each case. The isolated
benchmark disables MCP servers and cloud credentials, so it cannot quantify
the benefit of skipping MCP routing in a fully connected main instance.

## Comparison

All results below came from isolated backend processes with separate data
directories on the same Windows CPU machine. Each row is one run, so small
differences are observations rather than a statistically stable speedup.
The application waited for its background memory updates before returning the
HTTP response; `/perf` reports the earlier assistant-answer time.

| Same prompt, no MCP source | Before: Q4 default | After: Q4 optimized |
| --- | ---: | ---: |
| New CSV export estimate | 38.4 s, HTTP 200 | 34.6 s, HTTP 200 |
| Add amount column, revise estimate | 44.1 s, HTTP 200 | 55.0 s, HTTP 200 |
| Ask about yesterday's activity, with no activity source | 27.6 s, HTTP 200 | 37.5 s, HTTP 200 |
| Peak sampled Ollama process RSS | 2350 MiB | 2344 MiB |
| Peak summed CPU use (100% = one logical core) | 815% | 806% |

The stricter JSON prompt sometimes increases latency and tokens when no tool
router is needed. On an earlier baseline run, the activity answer failed with
HTTP 502 because the model emitted the malformed key `"citations:[]`; the
optimized run succeeded. The matched run above had no such failure on either
version, so one observation is not a failure-rate estimate.

With an isolated Worklog MCP server available, the *same* estimate request
gave the same visible 4–7-hour estimate on both versions. Before optimization
the router and answer consumed 3067 tokens and the HTTP request took 89.1 s;
after the explicit-estimate routing skip they consumed 861 tokens and took
36.4 s (59% less elapsed time for this case). Both returned HTTP 200. This
is the relevant speed improvement for the normal, MCP-connected app.

The installed `qwen2.5:3b` is Q4_K_M. The temporary
`qwen2.5:3b-instruct-q3_K_M` variant reduced Ollama's reported loaded size
from 2.02 to 1.70 GiB at the same 4096-token context. Its new-estimate
answer repeated placeholder text, and its follow-up failed with HTTP 502;
the variant was removed. Process RSS for that run cannot be compared directly
because Ollama had both Q3 and Q4 models loaded during sampling.

A temporary Q4 variant with `num_ctx 8192` raised Ollama's loaded size from
2.02 to 2.25 GiB. On the same short estimate prompt it returned HTTP 200 in
60.3 s, versus 34.6 s with 4096. The larger window had no visible quality
benefit for this prompt, so the application keeps 4096 and the temporary
variant was removed. A longer-history task may justify revisiting this choice.

An isolated main-chat RAG check reindexed one synthetic conversation, enabled
History, and asked how many hours a similar CSV task took. The final local
answer reported the prior **preliminary** 4–7-hour estimate, said actual
hours were unknown, and returned one server-verified citation to the chunk
containing those hours (HTTP 200, 20.3 s). The index and chat lived only in
the benchmark directory; no personal conversations were used.

## References

- [Ollama Modelfile parameters](https://docs.ollama.com/modelfile)
- [Ollama OpenAI compatibility, JSON mode](https://docs.ollama.com/api/openai-compatibility)
- [Ollama running model metadata](https://docs.ollama.com/api/ps)
