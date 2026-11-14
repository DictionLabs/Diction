# Live Text

The gateway can show your words while you talk (the app's **Live text** setting, under
Home → Keyboard) on any backend above. Both settings are off by default.

> Needs a gateway built after v15.0 (build from source until the next release) and the next
> Diction app release; on older versions the settings do nothing.


```yaml
    environment:
      DICTION_PREVIEW: chunked          # live text while you talk
      DICTION_SEGMENTED_FINAL: "true"   # long dictations finish in about a second
```

| Variable | Default | Description |
|----------|---------|-------------|
| `DICTION_PREVIEW` | `off` | `chunked`: send live text. The gateway re-transcribes the part since the last pause about once a second |
| `DICTION_PREVIEW_INTERVAL_MS` | `1000` | New audio needed before the next live text request. Minimum `250` |
| `DICTION_PREVIEW_URL` | (empty) | A separate OpenAI-compatible speech server for live text only (`DICTION_PREVIEW_MODEL`, `DICTION_PREVIEW_AUTH` go with it). Empty: the same backend |
| `DICTION_PREVIEW_MAX_CONCURRENT` | `2` | Live text requests in flight across the gateway, so many phones can't keep your GPU busy. A busy phone skips one update |
| `DICTION_SEGMENTED_FINAL` | `false` | `true`: the parts transcribed while you talked become the result, so after you tap stop only the audio since the last pause is left. Any failure falls back to one request for the whole recording |
| `DICTION_SEGMENT_FIRST_MIN_MS` | `0` | Recordings shorter than this are always transcribed whole, e.g. `15000`. `0` = 8 s |

Live text on Opus sessions needs ffmpeg, which the gateway image includes. The full list
(where audio is cut, and how) is in [`AGENTS.md`](../AGENTS.md#live-text-built-in-preview).
