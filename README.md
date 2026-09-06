# AI Advent Challenge #9

Репозиторий для **AI Advent Challenge #9** от Алексея Гладкова.

Подробности челленджа: https://mobiledeveloper.tech/ai_advent_9

## Как устроен репозиторий

- `main` — стабильная линия, накапливает продукт по мере прохождения дней челленджа.
- На каждый день — отдельная ветка `feature/wNN-dDD-slug` (например, `feature/w01-d02-structured-output`), смёрженная в `main` через Pull Request.
- Текст задания дня — в `days/wNN-dDD-slug.md`.
- Описание PR фиксирует, что конкретно реализовано в рамках задания этого дня.

---

# AI Work Intelligence Assistant — Week 1 Day 1 (LLM Fundamentals)

Software Task → AI Preliminary Estimate.

Пользователь описывает задачу разработки в веб-интерфейсе, React-фронтенд
отправляет её в Go-бэкенд, бэкенд обращается к корпоративному шлюзу LiteLLM,
валидирует JSON-ответ модели по схеме приложения и возвращает структурированную
предварительную оценку (summary, category, complexity, диапазон часов, risks,
assumptions).

Это **предварительная, обобщённая AI-оценка**. Истории пользователя пока нет,
поэтому ничего здесь не персонализировано. Общее видение продукта —
[`docs/concept.md`](docs/concept.md), точный скоуп этого дня —
[`days/w01-d01-llm-api-request.md`](days/w01-d01-llm-api-request.md).

## Стек

- Backend: Go (стандартная библиотека `net/http`, без фреймворка)
- Frontend: React + TypeScript + Vite, Tailwind CSS v4, shadcn/ui
- LLM: корпоративный шлюз LiteLLM (`https://llm.effective.land`),
  OpenAI-совместимый эндпоинт `/v1/chat/completions`

## Требования

- Go 1.22+
- Node.js 20+

## Запуск

### Backend

```
cd backend
cp .env.example .env
```

Заполнить `backend/.env`:

```
LITELLM_API_KEY=ваш-ключ
LITELLM_MODEL=название-модели
```

Запуск:

```
go run .
```

API слушает `http://localhost:8080` (переопределяется через `PORT`). Ключ
LiteLLM используется только на сервере и никогда не уходит в браузер.

### Frontend

```
cd frontend
npm install
npm run dev
```

Открыть `http://localhost:5173`. В dev-режиме Vite проксирует `/api/*` на
`http://localhost:8080`, поэтому нужны оба запущенных сервера.

## Проверка backend отдельно

```
curl -s http://localhost:8080/api/estimate \
  -X POST -H "Content-Type: application/json" \
  -d '{"task":"Обновить устаревшее Flutter-приложение до новой версии Flutter, обновить зависимости, исправить проблемы сборки под iOS и Android и подготовить новые билды."}'
```

## Заметки

- Нет persistence, нет auth, нет истории — этот этап это только один цикл
  запрос/ответ.
- Никогда не коммитить `.env`-файлы и не хардкодить ключ LiteLLM.
