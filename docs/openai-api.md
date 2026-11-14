# OpenAI API Compatibility

The gateway implements the OpenAI audio transcription API - any client that works against `api.openai.com/v1/audio/transcriptions` works against a Diction gateway.

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://your-server:8080/v1",
    api_key="anything",  # or a dk_ pairing key when DICTION_GATEWAY_AUTH=required
)

with open("audio.wav", "rb") as f:
    result = client.audio.transcriptions.create(
        file=f,
        model="small",            # or "DictionLabs/whisper-small-ct2"
        response_format="text",
    )
print(result)
```

Works with the Node SDK, LangChain, Flowise, n8n, or any tool that expects OpenAI's speech API.

**Supported:**

- `POST /v1/audio/transcriptions` - `file`, `model`, `language`, `prompt`, `response_format=json|text`
- `GET /v1/models` - returns an OpenAI-compatible `data[]` array plus a `providers[]` grouping consumed by the iOS app. HuggingFace IDs (`DictionLabs/whisper-small-ct2`, `nvidia/parakeet-tdt-0.6b-v3`) and short aliases (`small`, `medium`, `large-v3-turbo`, `parakeet-v3`) are both accepted. The older `Systran/*` and `deepdml/*` ids stay accepted as aliases, so existing scripts keep working.
- WebSocket `/v1/audio/stream` - used by the Diction app for low-latency streaming

**Not supported:**

- TTS (`/v1/audio/speech`)
- `response_format=verbose_json|srt|vtt` (no word-level timestamps)
- SSE streaming on REST (use WebSocket `/v1/audio/stream` instead)
- Model download/delete (`POST`/`DELETE /v1/models/{id}`)
- OpenAI Realtime API (`/v1/realtime`)

**Authentication:** with the default `DICTION_GATEWAY_AUTH=optional`, pass any non-empty string
as the API key - the gateway doesn't check it. With `required`, pass a paired key (`dk_...`, printed
by `docker exec diction-gateway gateway auth`) as the API key. See
[Pairing and Access Control](pairing.md).

**Error shape:** errors return `{"error":"<message>"}`, except a missing pairing key in required mode, which returns `{"error":"unauthorized","reason":"invalid_key","message":"..."}`, not OpenAI's nested `{"error":{"message":"...","type":"..."}}`. Most SDKs surface these as `HTTPError` rather than `APIError`.
