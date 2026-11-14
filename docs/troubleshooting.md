# Troubleshooting

## The stack will not start

| Error | Fix |
|-------|-----|
| `pull access denied` on gateway image | `docker logout` and retry - a stale login to Docker Hub or `ghcr.io` can shadow the anonymous pull |
| `no matching manifest for linux/arm64` on gateway image | ARM host pulling a gateway tag older than v14.1 - use `latest` |
| Gateway exits immediately | Parakeet container failed - check its logs |



<details>

## The test request fails

| Response | Cause |
|----------|-------|
| Connection refused | Gateway not running - `docker compose ps` |
| 502 Bad Gateway | Parakeet unreachable, still loading, or `DEFAULT_MODEL` names a service that isn't running |
| 400 Bad Request | Unsupported `response_format` (only `json` and `text`) |
| 404 Not Found | URL typo - path must be exactly `/v1/audio/transcriptions` |
| OOM / container crash | Not enough RAM - Parakeet needs about 2 GB |



<details>

## Which model answered? Timing headers

```bash
# Check timing headers
curl -sS -D - -o /dev/null \
  -X POST http://localhost:8080/v1/audio/transcriptions \
  -F "file=@test.wav" -F "model=parakeet-v3" | grep -i diction
```

That returns three headers, verified against a live gateway:

| Header | Meaning |
|--------|---------|
| `X-Diction-Whisper-Ms` | speech model inference latency in milliseconds |
| `X-Diction-Route-Model` | which backend actually served the request |
| `X-Diction-Route-Lang` | detected language, empty when detection didn't run |

`X-Diction-LLM-Ms` is added when AI cleanup runs. `X-Diction-Route-Model` is the quickest way to
confirm a model switch took effect, since an unrecognised model name silently falls back to
`DEFAULT_MODEL`.

## The app says "Gateway unreachable"

Check that the phone is on the same network as the server (or on Tailscale) and that `curl http://<your-ip>:8080/health` answers from another machine on that network. Plain `http://` to a LAN or Tailscale address works; the app does not need HTTPS.

## Requirements in detail

Parakeet uses about 2 GB of memory while it transcribes (1.25 GB image); the gateway needs under 100 MB (150 MB image). Both run on x86-64 (amd64) and ARM (arm64), so a Raspberry Pi 4 or 5 works. The gateway image is multi-arch from v14.1: if you pinned an older tag on ARM, move to `v14.1` or `latest`.
