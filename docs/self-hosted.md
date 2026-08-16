---
title: Self-Hosted Transcription
description: Run your own speech-to-text server and connect Diction to it. Audio stays on your network. Free, unlimited, open-source. Three setup paths including a faster engine for European languages
keywords: "self hosted speech to text iphone, whisper docker ios keyboard, self hosted whisper ios, run whisper at home iphone, home server dictation ios, whisper server ios keyboard, docker whisper iphone"
---

<img src="/illustration-self-hosted.svg" alt="Data servers" class="illustration" style="max-width: 480px; margin: 0 auto 2rem; display: block;" />

# Self-Hosted Transcription

Your server, your models, your rules. Run a Whisper server on your own hardware, paste its URL into the Diction app, and your audio never leaves your network.

Good for: regulated industries where audio cannot go to the cloud, people who already run servers at home, anyone who wants a specific model for a specific language or domain, and folks who refuse to hand their voice to someone else's infrastructure.

## How it works

Diction speaks the OpenAI transcription API directly. If your server accepts `POST /v1/audio/transcriptions`, Diction can talk to it. That's the whole contract.

You have three ways to run it.

### The simple way: whisper only

One container, no extras. Start any OpenAI-compatible Whisper server, point the app at its address.

```bash
git clone https://github.com/DictionLabs/Diction.git
cd diction
docker compose --profile small up -d
```

Pick a profile that matches the engine you want (`small`, `medium`, `large`, or `parakeet`). The compose file starts our gateway plus your chosen speech engine. If you want the absolute minimum and don't mind a short pause after you stop speaking, run just the Whisper container and skip the gateway entirely. Details in the [setup guide](/features/self-hosting-setup).

### The fast way: whisper plus the Diction gateway

Run our open-source gateway in front of whisper. It adds a WebSocket layer, so the app can stream your audio live while you're still talking. By the time you tap stop, the transcript is already coming back. The longer the dictation, the bigger the gap. Short phrases barely change.

Same compose file, same profile command, same URL paste into the app.

### The alternative: a faster engine for European languages

Whisper supports 99 languages, but if you mostly dictate in a European language there's a faster option. NVIDIA's speech engine is more accurate, roughly 10x faster, and uses less RAM. It supports 25 European languages. The setup guide covers both engines.

Full walkthrough: [Self-Hosting Setup Guide](/features/self-hosting-setup). Already running your own Whisper server? [Use Your Own Model](/features/custom-model).

## What you get

- **Audio stays on your network.** From your phone to your server and back. No third party in the middle.
- **Free, unlimited, no restrictions.** No Diction subscription needed. No word limits. No daily caps. No trial that expires.
- **Works with any Whisper-compatible server.** The app speaks the OpenAI transcription API directly. Use our default stack, use someone else's, roll your own.
- **AI parity.** Add a BYO LLM (`LLM_BASE_URL` + `LLM_MODEL`) and set `TEXT_ROUTES_OPEN=true`. Voice editing and suggestions work the same as on Diction One. Weak local models may not follow instructions well -- 7B or larger recommended.
- **Optional streaming.** Run our gateway in front of whisper and the app streams audio as you speak. Longer dictations are noticeably faster.
- **On-device fallback.** If your server is unreachable, Diction automatically retries using a local model on your iPhone. Your dictation is never lost to a network issue.
- **Run it anywhere.** Home server, NAS, Raspberry Pi for tiny models, cloud VM, behind a reverse proxy, over a VPN. If Docker runs there, Diction connects to it.
- **Pick your own model.** Run whatever speech model fits your use case. Your language, your accuracy requirements, your hardware.
- **Open-source gateway.** The gateway infrastructure is fully open source on GitHub. Inspect it, modify it, contribute to it.

## Best for

- You already run Docker at home and want transcription on your own hardware
- You work in a regulated industry where audio cannot leave your network
- You want a specific model for a specific language or domain
- You refuse to send voice data to someone else's cloud
- You already have a Whisper server running and just want an iOS keyboard that talks to it
