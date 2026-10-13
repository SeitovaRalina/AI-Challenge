"""Compare candidate Ollama models through the same API used by the app."""

import json
import sys
import time
from urllib import request


PROMPTS = [
    "Оцени добавление CSV-экспорта заказов с фильтрами по дате и складу. Верни JSON с полями summary, estimated_hours_min, estimated_hours_max.",
    "Оцени ту же задачу, но добавь проверку прав доступа к экспорту и тесты. Верни JSON с полями summary, estimated_hours_min, estimated_hours_max.",
]


def measure(base_url: str, model: str, prompt: str) -> dict:
    payload = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "temperature": 0.1,
        "max_tokens": 256,
        "response_format": {"type": "json_object"},
        "stream": False,
    }
    data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = request.Request(
        f"{base_url}/v1/chat/completions",
        data=data,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    started = time.monotonic()
    with request.urlopen(req, timeout=300) as response:
        result = json.load(response)
    elapsed = round(time.monotonic() - started, 2)
    choice = result["choices"][0]
    message = choice["message"]
    content = message.get("content") or ""
    try:
        parsed = json.loads(content)
    except json.JSONDecodeError:
        parsed = None
    valid = isinstance(parsed, dict) and all(
        key in parsed for key in ("summary", "estimated_hours_min", "estimated_hours_max")
    )
    return {
        "model": model,
        "seconds": elapsed,
        "finish_reason": choice.get("finish_reason"),
        "valid_estimate_json": valid,
        "usage": result.get("usage"),
        "answer": parsed if valid else None,
        "content_preview": content[:120] if not valid else None,
        "reasoning_chars": len(message.get("reasoning") or message.get("reasoning_content") or ""),
    }


def main() -> None:
    base_url = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:11435"
    models = sys.argv[2:] or ("qwen2.5:3b",)
    for model in models:
        for prompt in PROMPTS:
            try:
                print(json.dumps(measure(base_url, model, prompt), ensure_ascii=False), flush=True)
            except Exception as exc:
                print(json.dumps({"model": model, "error": str(exc)}, ensure_ascii=False), flush=True)


if __name__ == "__main__":
    main()
