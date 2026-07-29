---
title: Use Your Own Whisper Server
description: Already running a speech-to-text server? Connect Diction to it directly, or wrap it with the Diction gateway for streaming. Works with any OpenAI-compatible endpoint.
---

<img src="/illustration-custom-model.svg" alt="AI brain intelligence" class="illustration" style="max-width: 480px; margin: 0 auto 2rem; display: block;" />

# Use Your Own Whisper Server

Say you already have a speech-to-text server on your homelab. A beefy GPU box, a model fine-tuned for your language, something domain-specific, maybe just a newer release than the one in our default stack. You found Diction and want to point it at your existing setup without running more containers than you have to.

Good news: Diction speaks the standard OpenAI transcription API natively. It talks to your server directly. No gateway required.

The only question is whether you want streaming.

## Two paths

### Path 1: point Diction straight at your server

If your server implements `POST /v1/audio/transcriptions` with a multipart file upload (faster-whisper-server, whisper.cpp HTTP server, LocalAI in whisper mode, and most others do), paste its URL into Diction and you're done.

1. Open the Diction app
2. Switch to the **Self-Hosted** tab
3. Paste your server URL, for example `http://192.168.1.50:8000`
4. Start dictating

No extra containers, no compose file, no proxy.

**The trade-off:** no streaming. The app uploads your recording to your server after you stop speaking, your server transcribes, the text comes back. On short dictations you barely notice. On longer ones, there's a visible pause between the moment you tap stop and the text arriving. Whether that matters depends on how long your typical dictation is.

If you're on a GPU and transcription is already fast, Path 1 is probably all you need.

### Path 2: wrap it with the Diction gateway (streaming)

Run our open-source gateway in front of your existing server. It exposes a WebSocket endpoint the app uses to stream audio up as you speak. By the time you stop talking, the transcript is mostly ready.

You run just the gateway, pointed at your existing backend:

```yaml
services:
  gateway:
    image: ghcr.io/dictionlabs/gateway:latest
    ports:
      - "8080:8080"
    environment:
      CUSTOM_BACKEND_URL: http://192.168.1.50:8000
      CUSTOM_BACKEND_MODEL: your-model-name
```

```bash
docker compose up -d
```

Paste the gateway's address into Diction's **Self-Hosted** tab:

```
http://192.168.1.50:8080
```

The gateway forwards to your existing server and adds the streaming layer on top. Short phrases feel about the same as Path 1. Longer dictations are noticeably faster.

The gateway is open source. It runs as a pure proxy. No subscription, no account, no telemetry.

## Gateway options

These only apply to Path 2.

### Model name

```yaml
environment:
  CUSTOM_BACKEND_URL: http://my-server:8000
  CUSTOM_BACKEND_MODEL: your-model-name-here
```

The gateway injects `CUSTOM_BACKEND_MODEL` as the `model` form field on every forwarded request. If your server runs a single model and doesn't care which name it receives, omit `CUSTOM_BACKEND_MODEL` and the gateway will leave the field untouched.

### WAV-only backend

Some models only accept WAV audio. The gateway converts for you via ffmpeg:

```yaml
environment:
  CUSTOM_BACKEND_URL: http://my-model:5092
  CUSTOM_BACKEND_NEEDS_WAV: "true"
```

Audio arrives as 16 kHz mono WAV. Your model gets what it expects.

### Backend behind an API key

```yaml
environment:
  CUSTOM_BACKEND_URL: http://my-server:8000
  CUSTOM_BACKEND_AUTH: "Bearer sk-your-key-here"
```

The gateway injects the `Authorization` header on every forwarded request.

### All options

| Variable | Required | Description |
|----------|----------|-------------|
| `CUSTOM_BACKEND_URL` | Yes | Base URL of your server, e.g. `http://192.168.1.50:8000` |
| `CUSTOM_BACKEND_MODEL` | No | Model name to send in the request. Omit if your server runs a single model and doesn't require the field. |
| `CUSTOM_BACKEND_NEEDS_WAV` | No | Set to `"true"` if your server only accepts WAV audio. Gateway converts via ffmpeg. |
| `CUSTOM_BACKEND_AUTH` | No | Full `Authorization` header value, e.g. `Bearer sk-xxx`. |

## Which path should I pick?

- **You want the minimum fuss and your hardware is fast:** Path 1. Paste URL, done.
- **You care about perceived latency on longer dictations:** Path 2. One extra container, streaming on top.
- **You're on a GPU and transcription already takes under a second:** Path 1 is fine. Streaming barely helps when there's nothing to hide.
- **You're on CPU with a larger model:** Path 2 makes a real difference.

Both paths keep your audio on your network. Neither sends anything to Diction's servers.

## Requirements

- Your existing speech server reachable from the iPhone (Path 1) or from the gateway (Path 2)
- Docker on any machine, for Path 2
- For remote access without opening router ports, see [Cloudflare Tunnel, Tailscale, or ngrok](/features/self-hosting-setup#no-public-ip)
