---
title: "Self-Hosting Setup Guide"
description: Run your own speech-to-text server and connect Diction to it. GPU-first setup with the Diction gateway, plus CPU-only Whisper alternatives
---

<img src="/illustration-self-hosting-setup.svg" alt="Controller" class="illustration" style="max-width: 480px; margin: 0 auto 2rem; display: block;" />

# Self-Hosting Setup Guide

Run your own speech-to-text server, point the Diction app at it, start dictating. Your audio never touches our infrastructure.

Diction speaks the OpenAI transcription API (`POST /v1/audio/transcriptions`). Any server that implements it works. Below are three ways to set it up. If you have an NVIDIA GPU around, which most people running a self-hosted gateway do, start with Path 1.

## Path 1: NVIDIA GPU (recommended)

[Parakeet](https://hub.docker.com/r/dictionlabs/parakeet) transcribes a 5-second clip in well under a second on a consumer GPU. Models are baked into the image, so there's no download on first start. Covers 25 languages: English, Bulgarian, Croatian, Czech, Danish, Dutch, Estonian, Finnish, French, German, Greek, Hungarian, Italian, Latvian, Lithuanian, Maltese, Polish, Portuguese, Romanian, Slovak, Slovenian, Spanish, Swedish, Russian, and Ukrainian.

Install the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html) on the host first.

```yaml
# docker-compose.yml
services:
  parakeet:
    image: dictionlabs/parakeet:latest-int8
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: 1
              capabilities: [gpu]

  gateway:
    image: dictionlabs/gateway:latest
    ports:
      - "8080:8080"
    environment:
      DEFAULT_MODEL: parakeet-v3
    depends_on:
      - parakeet
```

```bash
docker compose up -d
```

Paste `http://your-server:8080` into the Diction app's **Self-Hosted** tab. A green dot confirms the endpoint is reachable. Start dictating.

Need a non-European language? Use Path 2 below instead.

## Path 2: Whisper + the Diction gateway (no GPU, streaming)

No GPU, or need a language Parakeet doesn't cover. Runs on any machine that can run Docker, CPU only.

```yaml
# docker-compose.yml
services:
  gateway:
    image: dictionlabs/gateway:latest
    ports:
      - "8080:8080"
    environment:
      DEFAULT_MODEL: small

  whisper-small:
    image: dictionlabs/whisper-server:latest-cpu
    volumes:
      - whisper-models:/home/ubuntu/.cache/huggingface/hub

volumes:
  whisper-models:
```

```bash
docker compose up -d
```

Paste `http://your-server:8080` into the Diction app's **Self-Hosted** tab. Slower than Path 1, but works everywhere.

The Diction gateway is fully open source. It runs as a pure proxy and streaming layer. It does not talk to our servers, does not require a subscription, and does not send any telemetry.

## Path 3: Whisper only, no gateway (simplest, limited)

The absolute minimum: one container, no gateway, no streaming.

```yaml
# docker-compose.yml
services:
  whisper:
    image: dictionlabs/whisper-server:latest-cpu
    ports:
      - "8000:8000"
    volumes:
      - whisper-models:/home/ubuntu/.cache/huggingface/hub

volumes:
  whisper-models:
```

```bash
docker compose up -d
```

Open the Diction app, switch to **Self-Hosted**, paste `http://your-server:8000`.

::: warning Path 3 does not work with this image today
The app talks to your Whisper server directly here and does not name a model in the request, so
the server has to already know which one to load. Speaches, the engine in
`dictionlabs/whisper-server`, wants the model per request and answers `422 Field required`
instead. Every dictation fails, quietly: the app falls back to on-device and still inserts text.

Use **Path 1** or **Path 2** above instead. The gateway names the model for you. Path 3 still
works against a server that pins its own model, such as an older `faster-whisper-server` image
with `WHISPER__MODEL` set.
:::

**The trade-off even when it works:** no streaming. The app waits until you stop speaking, uploads the whole recording to your server, and waits for Whisper to transcribe it. On short phrases that's fine. On longer dictations you'll see a visible pause after you tap stop.

## Choosing a model

Paths 2 and 3 support any Whisper model. Pick based on your hardware and what you're dictating.

| Model ID | Params | RAM | Notes |
|----------|--------|-----|-------|
| `DictionLabs/whisper-small-ct2` | 244M | ~850 MB | Recommended starting point. Fast on CPU, fine for most dictations. |
| `DictionLabs/whisper-medium-ct2` | 769M | ~2.1 GB | Better with accents and background noise. Slow on CPU, good on GPU. |
| `DictionLabs/whisper-large-v3-turbo-ct2` | 809M | ~2.3 GB | Highest accuracy. Manageable on modern CPUs, near-instant on GPU. |

These are our own CTranslate2 builds of OpenAI's Whisper checkpoints, published at
[huggingface.co/DictionLabs](https://huggingface.co/DictionLabs). Any other CTranslate2 Whisper
model works too.

For Path 2 (gateway), update `DEFAULT_MODEL` on the gateway service and make sure the Whisper service is named to match: `whisper-small`, `whisper-medium`, or `whisper-large-turbo`. The gateway injects the correct model ID into each request automatically.

Path 1 (Parakeet) uses a different engine with models baked into the image. No model selection needed.

The full compose file in the [GitHub repository](https://github.com/DictionLabs/Diction) puts each engine behind a profile. Pick one and start:

```bash
docker compose --profile small up -d      # Whisper small
docker compose --profile medium up -d     # Whisper medium
docker compose --profile large up -d      # Whisper large-v3-turbo
docker compose --profile parakeet up -d   # NVIDIA engine (European languages)
```

Set `DEFAULT_MODEL` on the gateway to match your chosen profile.

The three Whisper profiles download their model on first start, so the first `up` takes a few
minutes longer while a few hundred megabytes (or 1.6 GB for large) arrives. The compose file
handles this with a one-shot pull service per profile. The model lands in a named volume, so
it survives restarts and later `up` commands skip straight past it. The Parakeet profile has
nothing to download.

## Connecting the app

1. Open the Diction app
2. Switch to the **Self-Hosted** tab
3. Paste your server URL into **Endpoint URL**:

```
http://192.168.1.100:8080
```

Replace the address with your server's actual IP. A green dot next to the endpoint confirms it's reachable. Tap the mic and start dictating.

## No public IP?

You don't need to open ports on your router. Several free options connect your phone to a home server from anywhere:

- **[Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)**. Free, outbound-only connection. No port forwarding.
- **[Tailscale](https://tailscale.com/)**. Free WireGuard mesh VPN. Install on server and phone, connect from anywhere.
- **[ngrok](https://ngrok.com/)**. Instant public URL. Great for quick testing.

## Optional: API key

If your server is behind an API key (common with reverse proxies or hosted endpoints), enter it in the **API Key** field in the app's Self-Hosted settings. It's sent as a `Bearer` token with every request.

## Any Whisper endpoint works

None of the paths lock you to our containers. The Diction app and the gateway both talk the standard OpenAI transcription API. Anything that accepts `POST /v1/audio/transcriptions` with a file upload and returns a JSON transcript works:

- [Speaches](https://github.com/speaches-ai/speaches) (the engine behind `dictionlabs/whisper-server`)
- [whisper.cpp](https://github.com/ggerganov/whisper.cpp) HTTP server
- OpenAI's own Whisper API
- Any future model that speaks the same protocol

Point the **gateway** at them rather than the app. The gateway names the model on every request,
which the OpenAI spec requires and strict servers enforce. The app on its own leaves that field
out, so a server that does not pin its own model will reject it. Configure a third-party backend
with `CUSTOM_BACKEND_URL` and `CUSTOM_BACKEND_MODEL`, described in
[Use Your Own Model](/features/custom-model).

Already running one? See [Use Your Own Model](/features/custom-model).

## Requirements

- Any machine that runs Docker (home server, NAS, cloud VM, Raspberry Pi for tiny models). An NVIDIA GPU gets you Path 1; without one, Paths 2 and 3 run fine on CPU.
- iPhone on the same network, or reachable via tunnel or VPN

## Full configuration

The complete compose file with multiple model profiles, and all gateway environment variables, is in the [public GitHub repository](https://github.com/DictionLabs/Diction).

## AI features on self-hosted

When you configure `LLM_BASE_URL` and `LLM_MODEL`, your gateway gets full AI parity with Diction One:

- **Transcript cleanup** -- remove filler words, fix punctuation (existing, via `?enhance=true`)
- **Edit by voice** -- dictate an instruction; the gateway applies it to your text
- **Suggestions** -- tap a word and get 2-3 alternatives from the LLM

Set `TEXT_ROUTES_OPEN=true` on the gateway to enable the edit and suggest routes. Results depend on model quality -- 7B or larger is recommended for editing. Smaller models often do not follow instructions reliably.
