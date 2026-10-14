# Day 30: private Ollama service

The task-estimation chat remains on the development PC. Generation runs on a
Cloud.ru Ubuntu 24.04 VPS through a private HTTP service. The existing chat
store, profile, and projects remain in their original `CHAT_DATA_DIR`; neither
the backend nor its data is deployed to the VPS.

## Request path

```text
Browser -> local Go backend -> 127.0.0.1:11435 on PC
        -> SSH local forward -> 127.0.0.1:11435 on VPS (Nginx)
        -> 127.0.0.1:11434 on VPS (Ollama)
```

Only SSH port 22 is reachable from the internet. Ollama's local API has no
authentication, so neither HTTP port is bound to the VPS public interface.
Nginx permits model discovery (`GET /api/tags`, `GET /api/ps`) and generation
(`POST /api/chat`, `POST /v1/chat/completions`); other paths return 404.

The VPS has 2 vCPU with a 10% guaranteed share, 4 GB RAM, no GPU, and a 30 GB
disk. Ollama is limited to one loaded model, one parallel request, a queue of
four, and a default 4096-token context. Nginx allows an average of 12
generation requests per minute with a burst of five, then returns HTTP 429.
Its 256 KiB request-body limit is separate from the model context setting.
The application also has a 4096-token local context budget. Ollama's default
context is not a hard API cap: a direct API caller can override `num_ctx`.
The service uses rate limiting as its enforced basic restriction.

## VPS setup

Install Ollama from the [official Linux instructions](https://docs.ollama.com/linux)
and Nginx from Ubuntu packages. From the repository root on Windows, copy the
two reviewed configuration files to the VPS after substituting its public IP:

```powershell
scp -i "$env:USERPROFILE\.ssh\ai_challenge" deploy/w06-d30/ollama-override.conf deploy/w06-d30/private-ollama.nginx.conf ralina@<VPS_IP>:/home/ralina/
```

On the VPS, apply them and pull the selected model:

```sh
sudo install -d -m 0755 /etc/systemd/system/ollama.service.d
sudo install -m 0644 /home/ralina/ollama-override.conf /etc/systemd/system/ollama.service.d/private.conf
sudo systemctl daemon-reload
sudo systemctl restart ollama
ollama pull qwen2.5:3b
sudo apt-get update
sudo apt-get install -y nginx
sudo install -m 0644 /home/ralina/private-ollama.nginx.conf /etc/nginx/sites-available/private-ollama.conf
sudo ln -s /etc/nginx/sites-available/private-ollama.conf /etc/nginx/sites-enabled/private-ollama.conf
sudo unlink /etc/nginx/sites-enabled/default
sudo nginx -t
sudo systemctl restart nginx
```

Ollama and Nginx are both enabled as systemd services. The Nginx gateway is
the SSH tunnel's endpoint, not a public web server.

Verify the bind addresses on the VPS:

```sh
sudo ss -lntp
```

The expected listeners are `127.0.0.1:11434`, `127.0.0.1:11435`, and SSH on
port 22. Port 80 must not be listening. The cloud security group permits only
SSH inbound. Keep a virtual-console login available when changing networking.

## Connect from Windows

In a dedicated PowerShell window, keep this SSH tunnel running:

```powershell
ssh -N -o ExitOnForwardFailure=yes -o IdentitiesOnly=yes -i "$env:USERPROFILE\.ssh\ai_challenge" -L 127.0.0.1:11435:127.0.0.1:11435 ralina@<VPS_IP>
```

Replace `<VPS_IP>` with the VM's current public IP in Cloud.ru.

In another PowerShell window, check model discovery:

```powershell
curl.exe http://127.0.0.1:11435/api/tags
```

Set these values in the existing local `backend/.env` before starting the
usual `cd backend; go run .` and `cd frontend; npm run dev` processes:

```dotenv
OLLAMA_CHAT_URL=http://127.0.0.1:11435
OLLAMA_CHAT_DEPLOYMENT=private_vps
OLLAMA_CHAT_MODEL=qwen2.5:3b
```

Use the final model selected by the controlled VPS comparison for
`OLLAMA_CHAT_MODEL`. Do not change `CHAT_DATA_DIR` or move chat files. Keep
`OLLAMA_URL=http://localhost:11434` for local RAG embeddings. In an isolated
test instance, use a separate port and data directory instead of the normal
backend's. The UI model selector reads the deployment flag and displays
"Приватный VPS · Ollama".

## Verification

1. Confirm a chat response through the SSH forward with no LiteLLM key.
2. Send several sequential `/v1/chat/completions` requests and record HTTP
   status, latency, response shape, and memory use.
3. Send a short burst beyond the Nginx allowance and confirm HTTP 429.
4. Confirm `/api/delete` returns 404 through the gateway.
5. Confirm the VPS has no public HTTP listener, and the local backend reports
   `ollama_private_vps: true` in `/api/llm/providers`.

The SSH tunnel must stay open for the app to use the VPS. When it closes, the
existing local-provider unavailable state is shown; the app does not fall
back to LiteLLM. CPU-only generation on this small VPS may be substantially
slower than on the development PC.

## Model choice on this VPS

The same two Russian task-estimation prompts were sent through the private
OpenAI-compatible endpoint at temperature 0.1 and a 256-token response cap.
The reproducible harness is `deploy/w06-d30/benchmark.py`. On this CPU-only
host, `qwen2.5:3b` returned parseable estimate JSON in 40.36 and 40.94 seconds.
The estimates were 2–4 and 1–4 hours respectively; the lower revised minimum
despite added scope shows that format validity is not estimate accuracy.

`qwen3:4b` loaded at 3.2 GB and left only about 323 MiB available RAM. Its
first request took 104.97 seconds, used the full 256-token cap, and returned
no answer content because the tokens went to model reasoning. The second run
was stopped; the model was removed from the VPS. The selected service model
is `qwen2.5:3b`. A 7B Q4 model weighs 4.7 GB before runtime overhead and
does not fit the 4 GB host.

## Observed service checks

The SSH forward exposed `/api/tags` from the PC. A separate backend on port
18089 with its own data directory and no LiteLLM credentials reported
`ollama_available: true`, `ollama_private_vps: true`, and
`cloud_configured: false`. Two turns in one chat both returned HTTP 200 through
the forward. The first full turn took 2 minutes 32 seconds and produced a
4–7-hour estimate; the revision took 1 minute 25 seconds and produced 3–4
hours. The decrease after adding access-control work is a model-quality
limitation, not a service or routing failure. Both turns updated only the
isolated test chat and memory.

The loaded `qwen2.5:3b` occupied 2.2 GB with 4096 context and CPU execution;
the host still reported about 1.2 GiB available RAM. Twelve rapid malformed
generation requests returned HTTP 400 for admitted requests and HTTP 429 for
excess requests. The gateway returned 404 for `/api/delete`. `ss -lntp`
showed only SSH on a public bind address; both HTTP ports and Ollama's runner
were bound to loopback. Ollama and Nginx were enabled for system startup.
