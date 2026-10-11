# Local chat model check

## Scope and setup

The product already uses Ollama for `nomic-embed-text` embeddings. This check
adds a separate local **chat** model on the Windows development machine. The
machine has 15.7 GiB RAM, an Intel Core i5-12500H, and integrated Intel Iris
Xe graphics. The model is not an application provider yet; the goal here is to
prove local generation for software-task estimation questions. The selected
model, `qwen2.5:3b`, downloads about 1.9 GB. In its name, `2.5` identifies
the Qwen family version and `3b` identifies the roughly 3-billion-parameter
size; [`qwen2.5:1.5b`](https://ollama.com/library/qwen2.5) is the smaller
roughly 1.5-billion-parameter variant.

Ollama setup:

```powershell
ollama pull qwen2.5:3b
ollama list
ollama run qwen2.5:3b
```

For a reproducible HTTP check, use Ollama's local `/api/chat` endpoint. Every
request sets `stream` to `false` so one response contains both generated text
and timing/token counts. No application backend or LiteLLM credential is used.

```powershell
$ollamaUrl = 'http://127.0.0.1:11434/api/chat'
$model = 'qwen2.5:3b'
$body = @{
  model = $model
  stream = $false
  options = @{ num_predict = 400 }
  messages = @(@{ role = 'user'; content = 'Оцени в часах простую задачу: добавить кнопку очистки поиска в веб-приложении. Ответь кратко.' })
} | ConvertTo-Json -Depth 5
$result = Invoke-RestMethod -Uri $ollamaUrl -Method Post -ContentType 'application/json; charset=utf-8' -Body ([System.Text.Encoding]::UTF8.GetBytes($body))
$result.message.content
$result | Select-Object model,done,total_duration,prompt_eval_count,eval_count
```

## Three requests

1. Simple: `Оцени в часах простую задачу: добавить кнопку очистки поиска в веб-приложении. Ответь кратко.`
2. Medium: `Нужно добавить в React-приложение фильтр задач по проекту и статусу, сохранить выбор в URL и покрыть поведение тестами. Дай предварительный диапазон часов, риски и допущения.`
3. Complex: `Нужно перенести историю рабочих сессий из JSON в PostgreSQL без потери данных, сохранить работу существующего API, добавить миграцию и откат, учесть параллельные записи. Дай предварительный диапазон часов, разбивку на этапы, риски и вопросы перед оценкой. Не утверждай, что знаешь мою прошлую скорость работы.`

Each prompt is sent in a separate one-message conversation through the same
HTTP API, with the same model and request options. A nonempty Russian response
and `done: true` establish that the local model answered. The content is a
preliminary estimate, not a validated personal forecast.

## Observations

Two other model variants were checked before selecting a model for the three-request
proof. `qwen3:4b` generated 1,263 tokens for the simple task and took 145.9 s
on CPU, including an unexpectedly long reasoning trace inside the visible
content despite `think: false`; its final estimate was 0.5 h. At request
time, about 2 GiB of physical memory was free. This made it unsuitable for a
short, readable demo on this machine.

`qwen2.5:1.5b` answered the simple request in 3.1 s (`2 hours`). The medium
request took 11.4 s but gave three ranges in **days** despite an explicit
request for hours. The complex request took 16.9 s, generated 400 tokens,
ended with `done_reason: length`, and did not reach the requested hour range.
It proved local execution, but did not follow the estimation instructions
reliably enough for this product.

The selected `qwen2.5:3b` ran on CPU in an isolated Ollama instance bound to
`127.0.0.1:11435`, with its own copy of the model store and
`OLLAMA_NO_CLOUD=1`. The Ollama server log confirmed `Ollama cloud disabled:
true`. Its `/api/version` endpoint returned `0.40.2`; `ollama list` showed
`qwen2.5:3b`; `ollama run qwen2.5:3b` also returned a Russian one-sentence
answer. The three HTTP checks used `stream: false` and `num_predict: 400`:

| Request | Wall time | Prompt / output tokens | Finish | Observed answer |
| --- | ---: | ---: | --- | --- |
| Simple | 6.2 s | 65 / 31 | `stop` | Said the task takes under 15 minutes, despite the request for hours. |
| Medium | 27.6 s | 88 / 400 | `length` | Gave an approximate 16-hour total, risks and assumptions, but no range; the response was cut off. |
| Complex | 29.4 s | 128 / 400 | `length` | Listed migration stages with hour estimates, but did not finish with a total range or all requested questions. |

With only `num_predict` raised to 800, the same prompts took 1.5 s, 56.2 s,
and 57.6 s. The medium and complex answers still ended with `length` because
the model added unrequested sections; the complex answer included a 10–20 h
total that did not match the sum of its listed stage ranges. This is a local
execution proof, not evidence that this model is ready to give reliable task
estimates. Its instruction-following and estimate consistency remain visible
limitations.

The [`/api/chat` response](https://docs.ollama.com/api/chat) supplies the
completion status, duration and token counts used above. The checks used no
LiteLLM API key and sent no request to the company gateway.
