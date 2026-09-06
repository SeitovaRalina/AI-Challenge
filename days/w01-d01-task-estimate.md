# Project: AI Work Intelligence Assistant

## Product vision

We are building a personal AI-powered work intelligence system for software developers.

The product should eventually help a developer understand where their working time goes, reconstruct timesheets from real activity, analyze personal productivity patterns, and estimate future software tasks based on their own historical performance.

This is NOT a generic AI chat application and NOT a generic task manager.

The core product idea is:

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

The application will eventually integrate sources such as:

- GitHub activity;
- AI coding-agent sessions;
- Yandex Calendar meetings;
- manual tasks;
- Solidtime;
- potentially desktop activity collected by a local client.

Future product areas will include:

1. Timesheet
   - reconstruct work sessions;
   - detect potentially untracked work;
   - allow users to edit, merge, split, reject, or accept suggestions;
   - eventually sync approved entries to Solidtime.

2. Analytics
   - time by project;
   - time by activity type;
   - focus sessions;
   - idle periods;
   - context switching;
   - meeting load;
   - productive hours;
   - weekly/monthly trends.

3. Estimates
   - accept a new software-development task;
   - analyze its characteristics;
   - later retrieve similar historical tasks;
   - estimate how long THIS USER is likely to need;
   - show uncertainty instead of pretending the estimate is exact.

4. Forecast
   - planned workload;
   - meetings;
   - available capacity;
   - predicted effort;
   - risks of overload.

The project is being developed incrementally as part of a seven-week AI Challenge:

1. LLM fundamentals
2. Agents and context
3. Agent/state optimization
4. MCP
5. RAG
6. Local AI
7. Pipeline and automation

Each week's implementation must remain useful to the final product.
Do not introduce AI features purely to satisfy the course topic.

---

# Technical direction

This project intentionally uses a stack that is relatively new to the developer.

Backend:
- Go

Frontend:
- React
- TypeScript
- Vite

UI:
- Tailwind CSS
- shadcn/ui where appropriate

Do NOT introduce Python for Day 1.

Do NOT introduce:
- PostgreSQL;
- Redis;
- Docker;
- authentication;
- MCP;
- RAG;
- background workers;
- desktop tracking;
- Solidtime integration;
- GitHub integration;
- calendar integration.

Those belong to later iterations.

Keep the current implementation deliberately small.

---

# LLM provider

Use the company's LiteLLM gateway:

https://llm.effective.land

LiteLLM exposes OpenAI-compatible endpoints.

Reference:
https://docs.litellm.ai/docs/supported_endpoints

Authentication is provided using an API key.

Never hardcode the key.

Use environment variables:

LITELLM_API_KEY
LITELLM_MODEL

The LiteLLM base URL can be stored as configuration, but the default for this project is:

https://llm.effective.land

Never commit secrets.

---

# Day 1 goal — LLM Fundamentals

The course assignment is:

- send a request to an LLM through an API;
- receive the response;
- display it in a CLI or simple Web interface.

For this project, implement the Web-interface version.

The Day 1 feature is:

    Software Task → AI Preliminary Estimate

This is the first primitive version of the future personal estimation feature.

The user enters a software-development task.

Example:

"Upgrade a legacy Flutter application to a newer Flutter version,
update dependencies, fix iOS and Android build issues, and prepare
new builds."

The frontend sends this task to the Go backend.

The Go backend sends it to LiteLLM.

The LLM analyzes the task and returns structured information.

Display the result in the UI.

---

# Important semantic limitation

At Day 1, the estimate is NOT personalized.

There is no historical user data yet.

The application must clearly describe the output as a preliminary AI estimate.

Do not claim things such as:

- "Based on your previous work"
- "You usually need..."
- "Your historical average..."

Personal estimates will be implemented later using historical data and RAG.

---

# Expected LLM output

Ask the model to return structured JSON with approximately this schema:

{
  "summary": "string",
  "category": "string",
  "complexity": "low | medium | high",
  "estimated_hours_min": number,
  "estimated_hours_max": number,
  "risks": ["string"],
  "assumptions": ["string"]
}

Validate the response on the backend.

Do not expose raw LiteLLM responses directly to the frontend.

If the model returns invalid data, return a controlled application error.

---

# API

Keep the backend minimal.

Expected application endpoint:

POST /api/estimate

Request:

{
  "task": "..."
}

Response:

{
  "summary": "...",
  "category": "...",
  "complexity": "medium",
  "estimated_hours_min": 4,
  "estimated_hours_max": 7,
  "risks": [...],
  "assumptions": [...]
}

Use idiomatic Go, but do not overengineer.

A small handler/service split is acceptable.
A large clean-architecture/domain-driven abstraction is NOT needed for Day 1.

Use normal HTTP status codes.

Handle:
- empty task;
- missing configuration;
- LiteLLM authentication error;
- LiteLLM unavailable;
- invalid model output.

Never send the LiteLLM API key to the browser.

---

# Frontend UX

Create a polished first screen for the future product rather than a generic LLM demo.

This application will eventually become a work analytics dashboard, so establish an appropriate product visual language now.

The initial page should contain:

- compact product identity/header;
- a clear page title related to task estimation;
- task textarea;
- an "Estimate task" primary action;
- loading state;
- error state;
- structured result view.

The result should visually distinguish:

- estimated range;
- complexity;
- category;
- task summary;
- risks;
- assumptions.

Do not build chat bubbles.

This is not a chatbot UI.

Do not create a generic landing page with a giant marketing hero.
The user should arrive directly at a useful application screen.

Aim for a modern productivity/developer-tool interface:
- information-dense but calm;
- excellent typography;
- clear hierarchy;
- restrained use of color;
- strong spacing;
- responsive layout;
- good empty/loading/error states.

The future product will contain dashboards similar in spirit to productivity analytics tools, so the UI foundation should be suitable for charts, timelines, statistics, and weekly views later.

---

# Frontend Agent Skills — REQUIRED BEFORE IMPLEMENTATION

Before designing or implementing the frontend, install/load the following frontend-specific Agent Skills.

Do not install unrelated skills just because they are popular.

Required:

1. frontend-design
   Publisher: Anthropic

2. vercel-react-best-practices
   Publisher: Vercel Labs

3. web-design-guidelines
   Publisher: Vercel Labs

4. design-taste-frontend
   Publisher: Leonxlnx

These were selected specifically for frontend design, React implementation quality, web UX/accessibility, and visual taste.

Use the installed skills during implementation rather than merely installing them.

If the current agent environment uses the Agent Skills CLI, install the relevant repositories/skills using their supported installation mechanism.

For Vercel Agent Skills, the repository supports:

npx skills add vercel-labs/agent-skills

For design-taste-frontend:

npx skills add https://github.com/Leonxlnx/taste-skill --skill "design-taste-frontend"

For Anthropic skills, use the supported Agent Skills / Claude plugin installation mechanism available in the current environment.

If installation requires choosing from several skills, install only the frontend/design skill required for this task.

Do not install:
- React Native skills;
- mobile UI skills;
- image-generation skills;
- deployment skills;
- database skills;
- unrelated agent skills.

After loading the skills, use them to establish a coherent design direction before writing the final UI.

---

# Visual constraints

Avoid stereotypical AI-generated UI.

In particular, avoid:
- excessive gradients;
- glowing purple/blue backgrounds;
- oversized rounded cards everywhere;
- meaningless badges;
- excessive pill-shaped controls;
- huge marketing headlines;
- decorative charts with fake data;
- fake productivity statistics;
- fake historical estimates.

There is no user history yet, therefore do NOT show invented analytics data.

Only show real information returned by the current estimate request.

A subtle placeholder/navigation structure for future sections is acceptable, but do not implement fake functionality.

---

# Suggested repository structure

Keep it simple.

root/
├── frontend/
│   └── React + TypeScript + Vite application
│
├── backend/
│   └── Go API
│
├── .gitignore
└── README.md

Do not create unnecessary services or packages yet.

---

# Day 1 acceptance criteria

Day 1 is complete when:

1. The frontend runs locally.
2. The Go backend runs locally.
3. The user can enter a real software-development task.
4. The frontend sends it to the Go backend.
5. The Go backend makes a real authenticated request to:
   https://llm.effective.land
6. LiteLLM returns an LLM response.
7. The backend converts/validates the response into the application's structured estimate format.
8. The frontend renders the result clearly.
9. Loading and API failure states work.
10. No secrets are present in frontend code or committed files.
11. The implementation remains within Day 1 scope.

---

# Work process

Before coding:

1. Inspect the existing repository.
2. Preserve useful existing files unless they directly conflict with the new stack.
3. Identify what the previous Python prototype contains before replacing anything.
4. Install/load the required frontend skills.
5. Create a short implementation plan.
6. State the proposed frontend visual direction in a few sentences.

Then implement.

After implementation:

1. Run formatting.
2. Run frontend type checking/build.
3. Run Go tests or at minimum `go test ./...`.
4. Start both applications if the environment allows it.
5. Perform one real LiteLLM request if credentials are available locally.
6. Do not invent credentials or model aliases.
7. Fix errors found during verification.
8. Update README with exact local startup instructions.

Do not expand into later-week features.
The goal is one small but production-shaped vertical slice of the future product.