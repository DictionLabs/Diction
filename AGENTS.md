# Diction Gateway -- Agent Reference

Machine-legible reference for the Diction self-hosted gateway. Covers routes, wire formats,
environment variables, capabilities, and what the gateway can and cannot do.

## Overview

The gateway is a Go HTTP service that sits between the Diction iOS app and one or more
OpenAI-compatible speech-to-text backends. It handles:

- WebSocket streaming for low-latency transcription
- HTTP transcription fallback
- Optional LLM post-processing (transcript cleanup, voice editing, suggestions)
- Optional trial token auth (Diction One -- not for self-hosters)

> WARNING: TEXT_ROUTES_OPEN=false (default). /v1/text/* routes return 403 until
> you explicitly set TEXT_ROUTES_OPEN=true or AUTH_ENABLED=true. This is a
> deliberate speed bump -- not a security control. Set it before calling those routes.

## Routes

### GET /health

Health check. Returns 200 when the gateway is up.

```
Response: 200 OK
Body: "ok"
```

### GET /v1/models

Lists configured speech backends (OpenAI-compatible + Diction legacy grouping).
Also includes a top-level capabilities object describing what this gateway instance
can do (additive -- never removes data[] or providers[]).

```
Response: 200 OK
Content-Type: application/json

{
  "object": "list",
  "data": [
    { "id": "nvidia/parakeet-tdt-0.6b-v3", "object": "model", "created": 0, "owned_by": "nvidia" }
  ],
  "providers": [
    {
      "id": "parakeet",
      "name": "NVIDIA Parakeet",
      "models": [{ "id": "parakeet-v3", "name": "Parakeet v3", "description": "...", "available": true }]
    }
  ],
  "capabilities": {
    "llm": true,
    "text_process": true,
    "text_suggest": true
  }
}
```

**capabilities object:**

| Field | Type | Meaning |
|-------|------|---------|
| `llm` | bool | LLM_BASE_URL + LLM_MODEL are set and LLM is active |
| `text_process` | bool | /v1/text/process is open (llm=true AND TEXT_ROUTES_OPEN=true, or auth=true) |
| `text_suggest` | bool | /v1/text/suggest is open (same condition as text_process) |

### POST /v1/trial

Issues a trial token for Diction One cloud subscription. Not useful for self-hosters
(requires TRIAL_SECRET to be configured with Diction's server-side secret).

```
Request body: {"device_id": "<UUID>"}
Response 200: {"token": "<hmac-token>", "expires_at": "<RFC3339>"}
Response 503: {"error":"trial_not_configured"}  -- TRIAL_SECRET not set
Response 409: {"error":"trial_already_used"}  -- trial already consumed
```

### POST /v1/audio/transcriptions

Transcribes an audio file. OpenAI-compatible multipart form upload.

```
Request: multipart/form-data
  file=<audio>
  model=<model-id>        (optional, defaults to DEFAULT_MODEL)
  language=<bcp47>        (optional)
  prompt=<string>         (optional, Whisper prompt hint)
  response_format=json|text  (optional, default json)

Response 200:
  Content-Type: application/json
  X-Diction-Whisper-Ms: <int>
  X-Diction-Route-Model: <model-id>
  X-Diction-LLM-Ms: <int>  (only when LLM ran)
  Body: {"text": "<transcript>"}
```

Append `?enhance=true` to request LLM cleanup. If LLM is not configured,
the raw transcript is returned -- transcription never fails due to LLM issues.

### WS /v1/audio/stream

WebSocket streaming transcription. Used by the Diction iOS app for live transcription
as you speak. Not part of any standard API. Binary audio frames in, JSON text frames out.

Not suitable for scripting -- use /v1/audio/transcriptions for batch use.

### POST /v1/text/process

**Plaintext JSON -- no E2E encryption. Bearer token optional.**

Applies LLM post-processing to text with an explicit intent. Used by the iOS app
for voice editing and transcript cleanup outside the transcription flow.

Requires LLM to be configured (LLM_BASE_URL + LLM_MODEL) and either:
- TEXT_ROUTES_OPEN=true, or
- AUTH_ENABLED=true with a valid bearer token

Returns 403 {"error":"text_routes_closed"} if neither condition is met.
Returns 503 {"error":"llm_not_configured"} if LLM env vars are not set.
Returns 400 {"error":"e2e not supported on this gateway"} if X-Diction-E2E header is present.

```
Request:
  POST /v1/text/process?intent=<intent>
  Content-Type: application/json
  Authorization: Bearer <token>  (optional)

  {
    "text": "<text or instruction>",
    "context": "{\"before\":\"...\",\"after\":\"...\",\"selected\":\"...\",\"customWords\":[...]}"
  }

intent values:
  (empty) or "transcribe"  -- cleanup prompt, text is the transcript
  "edit"                   -- edit prompt, text is the spoken instruction
  "edit-selected"          -- edit-selected prompt, applies instruction to selected text

Response 200:
  {"text": "<result>", "mode": "<intent>"}

On edit failure:
  "edit"          --> 500 {"error":"processing failed"}
  "edit-selected" --> 200 {"text":"<original-selected>","mode":"edit-selected","status":"failed"}
  transcribe      --> 200 {"text":"<original-text>","mode":"transcribe"}  (raw fallback)
```

### POST /v1/text/suggest

**Plaintext JSON -- no E2E encryption. Bearer token optional.**

Asks the LLM for 2-3 alternative phrasings of the selected text.
Always soft-fails: returns {"suggestions":[]} on any LLM error.

Same auth requirements as /v1/text/process.

```
Request:
  POST /v1/text/suggest
  Content-Type: application/json

  {
    "selected": "<selected text>",
    "before": "<context before selection>",
    "after": "<context after selection>",
    "customWords": ["word1", "word2"]
  }

Response 200:
  {"suggestions": ["alt 1", "alt 2", "alt 3"]}

Response 503:
  {"error": "llm_not_configured"}
```

## Environment Variables

### Core

| Variable | Default | Description |
|----------|---------|-------------|
| `GATEWAY_PORT` | `8080` | Port the gateway listens on |
| `DEFAULT_MODEL` | `small` | Fallback model when none is specified in the request |
| `MAX_BODY_SIZE` | `209715200` | Max request body size in bytes (200 MB) |
| `AUTH_ENABLED` | `false` | Require bearer token on audio routes. Do not set for self-hosted installs -- see README |
| `BUNDLE_ID` | `one.diction` | iOS bundle ID checked in JWS tokens (Diction One only) |
| `TRIAL_SECRET` | (empty) | Hex-encoded 32-byte HMAC key for trial tokens (Diction One only) |
| `TRIAL_DB_PATH` | `/data/trials.json` | Path to trial store |
| `TRIAL_DURATION` | `24h` | Duration for new trial grants |

### Speech Backend

| Variable | Description |
|----------|-------------|
| `CUSTOM_BACKEND_URL` | URL of a custom OpenAI-compatible STT backend |
| `CUSTOM_BACKEND_MODEL` | Model ID forwarded to the custom backend |
| `CUSTOM_BACKEND_AUTH` | Authorization header forwarded to the custom backend |
| `CUSTOM_BACKEND_NEEDS_WAV` | Set to `true` if the backend only accepts WAV |
| `CUSTOM_BACKEND_CANONICAL_ID` | HuggingFace-style ID to advertise in /v1/models |

### LLM (AI Post-processing)

Both LLM_BASE_URL and LLM_MODEL must be set or the LLM feature stays off.

| Variable | Default | Description |
|----------|---------|-------------|
| `LLM_BASE_URL` | (empty) | OpenAI-compatible chat completions endpoint, e.g. `https://api.openai.com/v1` |
| `LLM_MODEL` | (empty) | Model identifier, e.g. `gpt-4o-mini` |
| `LLM_API_KEY` | (empty) | Bearer token. Not needed for local Ollama |
| `LLM_REASONING_EFFORT` | (empty) | OpenAI reasoning effort: `none`, `low`, `medium`, `high` |
| `LLM_PROMPT` | (see default) | System prompt for transcript cleanup. String or `/path/to/file` |
| `LLM_PROMPT_EDIT` | (see default) | System prompt for voice-edit intent |
| `LLM_PROMPT_EDIT_SELECTED` | (see default) | System prompt for edit-selected intent |
| `LLM_PROMPT_SUGGEST` | (see default) | System prompt for suggest intent |
| `TEXT_ROUTES_OPEN` | `false` | Set to `true` to open /v1/text/* when AUTH_ENABLED=false |

## Default Prompts

When a prompt env var is empty (or file read fails), the gateway uses these built-in defaults:

**LLM_PROMPT (cleanup):**
```
You are a transcript cleanup tool. Fix grammar, punctuation, and remove filler words. Return only the corrected text, nothing else.
```

**LLM_PROMPT_EDIT:**
```
You are a text editor. Apply the user's spoken instruction to the text. Return only the edited result, nothing else.
```

**LLM_PROMPT_EDIT_SELECTED:**
```
You are a text editor. Apply the user's spoken instruction to the selected portion of text. Return only the edited selection, nothing else.
```

**LLM_PROMPT_SUGGEST:**
```
Suggest 2-3 concise alternative phrasings or corrections for the selected text. Return a JSON array of strings only, no explanation.
```

## What the Gateway Can and Cannot Do

**Can:**
- Transcribe speech via any OpenAI-compatible STT backend
- Stream live transcription over WebSocket
- Clean up transcripts using a BYO LLM (cleanup, edit, edit-selected)
- Suggest alternative phrasings (suggest intent)
- Probe health of speech backends
- Issue and verify trial tokens (Diction One only)

**Cannot:**
- E2E encrypt text routes -- /v1/text/* is plaintext only. E2E is a Diction cloud feature.
- Summarize arbitrary text -- summarize is a Diction cloud-only feature.
- Store audio -- audio is transcribed and discarded.
- TTS -- /v1/audio/speech is not implemented.
- SSE streaming on REST -- use WebSocket /v1/audio/stream instead.

## Wire Format Notes

- /v1/text/* endpoints are plaintext JSON. The X-Diction-E2E header must never appear on requests to these routes.
- /v1/audio/transcriptions and /v1/audio/stream may be E2E-encrypted when called by the Diction iOS app pointed at diction.cloud. Self-hosted gateways will never see E2E requests in normal operation.
- Error shape: `{"error":"<code>"}` for most errors. Auth failures: `{"error":"unauthorized","reason":"...","message":"..."}`.
- The suggest endpoint always returns 200 with `{"suggestions":[]}` on LLM error (soft-fail). Never 5xx.
