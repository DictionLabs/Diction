# Speech models

The default model, Parakeet, runs on any CPU and covers 25 European languages. This page shows how to run Whisper instead (99 languages) on a CPU or an NVIDIA GPU, and how to put the gateway in front of a speech server you already have.

# Swap the Speech Model

Set `DEFAULT_MODEL` on the gateway to the model you run. The gateway finds each backend by its
service name, so the service name has to match the table:

| `DEFAULT_MODEL` | Service name | Weights served | RAM | Notes |
|-----------------|--------------|----------------|-----|-------|
| `parakeet-v3` | `parakeet` | baked into the image | ~2 GB | 25 European languages; CPU only, fast |
| `small` | `whisper-small` | `DictionLabs/whisper-small-ct2` | ~850 MB | 99 languages; best Whisper for CPU |
| `medium` | `whisper-medium` | `DictionLabs/whisper-medium-ct2` | ~2.1 GB | More accurate, slower on CPU |
| `large-v3-turbo` | `whisper-large-turbo` | `DictionLabs/whisper-large-v3-turbo-ct2` | ~2.3 GB | Highest accuracy; slow on CPU, fast on GPU |

If the named service isn't running, every request fails with `502`. An unrecognised model name is
not an error: the gateway falls back to `DEFAULT_MODEL`, so a typo transcribes successfully with
the wrong model rather than telling you. Check the `X-Diction-Route-Model` response header to see
which backend actually served a request.

To run `medium` or `large-v3-turbo`, take the [Whisper on CPU](speech-models.md#whisper-on-cpu-99-languages) file
and replace `small` with the model's service name and weights in both the server and its `-pull`
service. The whisper server does **not** fetch models on demand, so every Whisper model needs its
pull service. The weights are CTranslate2 builds of OpenAI's checkpoints, published at
[huggingface.co/DictionLabs](https://huggingface.co/DictionLabs), so the models your server pulls
come from a namespace we control rather than a third party's conversion.

**Shortcut:** this repo's [`docker-compose.yml`](../docker-compose.yml) has every model as a compose
profile, all on CPU, with pairing and the `/data` volume already set up. It defaults to Parakeet;
for a Whisper profile, pass the matching `DEFAULT_MODEL`:

```bash
curl -O https://raw.githubusercontent.com/DictionLabs/Diction/main/docker-compose.yml
docker compose --profile parakeet up -d
DEFAULT_MODEL=small docker compose --profile small up -d     # or medium, or large-v3-turbo with --profile large
```

```bash
docker compose up -d   # after editing; recreates only the changed container
```

# Whisper on CPU (99 languages)

Parakeet covers 25 European languages. For the other 74, run Whisper instead. It runs on CPU anywhere Docker does, slower than Parakeet.

```yaml
services:
  # The whisper server runs as uid 1000. A volume left behind by the older root-based
  # images is owned by root, so without this the server cannot write and every model
  # download fails with a permission error. No-op on a fresh install.
  whisper-models-init:
    image: dictionlabs/whisper-server:latest-cpu
    user: root
    volumes:
      - whisper-models:/cache
    entrypoint: ["sh", "-c", "chown -R 1000:1000 /cache"]
    restart: "no"

  whisper-small:
    image: dictionlabs/whisper-server:latest-cpu
    container_name: diction-whisper-small
    restart: unless-stopped
    depends_on:
      whisper-models-init:
        condition: service_completed_successfully
    volumes:
      - whisper-models:/home/ubuntu/.cache/huggingface/hub
    healthcheck:
      test: ["CMD", "curl", "-fsS", "-o", "/dev/null", "http://localhost:8000/health"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 120s

  # The whisper server never downloads a model on demand: it answers "not installed
  # locally" instead of fetching. Without this one-shot pull the stack starts cleanly
  # and then fails every transcription. Idempotent, exits once the model is on disk.
  whisper-small-pull:
    image: dictionlabs/whisper-server:latest-cpu
    depends_on:
      whisper-small:
        condition: service_healthy
    entrypoint: ["curl", "-fsS", "-X", "POST",
                 "http://whisper-small:8000/v1/models/DictionLabs/whisper-small-ct2"]
    restart: "no"

  gateway:
    image: dictionlabs/gateway:latest
    container_name: diction-gateway
    restart: unless-stopped
    ports:
      - "8080:8080"
    depends_on:
      - whisper-small
    environment:
      DEFAULT_MODEL: small
    volumes:
      - gateway-data:/data

volumes:
  whisper-models:
  gateway-data:
```

The `whisper-models` volume persists the model weights (~500 MB for `small`) so they survive container rebuilds. The first `up` takes a few minutes longer than later ones while `whisper-small-pull` downloads the model - it exits as soon as that finishes, and subsequent starts skip straight past it.

```bash
docker compose up -d
```

| Error | Fix |
|-------|-----|
| `health: starting` for > 3 minutes | Model still downloading - `docker compose logs -f whisper-small` |
| Every transcription fails with "not installed locally" | `whisper-small-pull` didn't run or failed - `docker compose logs whisper-small-pull` |
| Gateway exits immediately | Whisper container failed - check its logs |

Test it the same way as the [setup test step](../README.md#set-up-your-own-server), with `-F "model=small"` instead of `parakeet-v3`.

# NVIDIA GPU: Whisper large-v3-turbo

With an NVIDIA GPU, Whisper large-v3-turbo is fast and covers all 99 languages:

| | Parakeet TDT 0.6B v3 (CPU) | Whisper Large-v3-turbo (GPU) |
|---|---|---|
| WER (English) | ~6.3% | 7.4% |
| Latency | Sub-second to ~1s | Under 2s |
| Memory | ~2 GB RAM | ~2.3 GB VRAM |
| Languages | 25 European | 99 |

Install the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html) on the host first.

```yaml
services:
  whisper-models-init:
    image: dictionlabs/whisper-server:latest-cuda
    user: root
    volumes:
      - whisper-models:/cache
    entrypoint: ["sh", "-c", "chown -R 1000:1000 /cache"]
    restart: "no"

  whisper-large-turbo:
    image: dictionlabs/whisper-server:latest-cuda
    container_name: diction-whisper-large-turbo
    restart: unless-stopped
    depends_on:
      whisper-models-init:
        condition: service_completed_successfully
    volumes:
      - whisper-models:/home/ubuntu/.cache/huggingface/hub
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: 1
              capabilities: [gpu]
    healthcheck:
      test: ["CMD", "curl", "-fsS", "-o", "/dev/null", "http://localhost:8000/health"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 120s

  # Installs the model on first start; the server never downloads one on its own.
  whisper-large-turbo-pull:
    image: dictionlabs/whisper-server:latest-cuda
    depends_on:
      whisper-large-turbo:
        condition: service_healthy
    entrypoint: ["curl", "-fsS", "-X", "POST",
                 "http://whisper-large-turbo:8000/v1/models/DictionLabs/whisper-large-v3-turbo-ct2"]
    restart: "no"

  gateway:
    image: dictionlabs/gateway:latest
    container_name: diction-gateway
    restart: unless-stopped
    ports:
      - "8080:8080"
    depends_on:
      - whisper-large-turbo
    environment:
      DEFAULT_MODEL: large-v3-turbo
    volumes:
      - gateway-data:/data

volumes:
  whisper-models:
  gateway-data:
```

The first `up` downloads ~1.6 GB of model weights into the volume; later starts skip it.

| Error | Fix |
|-------|-----|
| `could not select device driver "nvidia"` | NVIDIA Container Toolkit isn't installed, or Docker wasn't restarted after installing it |
| Every transcription fails with "not installed locally" | `whisper-large-turbo-pull` didn't run or failed - check its logs |

# Already Have a Voice Server?

Keep it. Use `CUSTOM_BACKEND_URL` to put the Diction Gateway in front of your existing server for WebSocket streaming and end-to-end encryption:

```yaml
services:
  gateway:
    image: dictionlabs/gateway:latest
    container_name: diction-gateway
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      CUSTOM_BACKEND_URL: http://your-existing-server:8000
      CUSTOM_BACKEND_MODEL: DictionLabs/whisper-small-ct2
    volumes:
      - gateway-data:/data

volumes:
  gateway-data:
```

| Variable | Description |
|----------|-------------|
| `CUSTOM_BACKEND_AUTH` | Authorization header forwarded to your backend, e.g. `Bearer sk-xxx` |
| `CUSTOM_BACKEND_NEEDS_WAV` | Set to `"true"` if your backend only accepts WAV - the gateway converts with ffmpeg |
| `CUSTOM_BACKEND_CANONICAL_ID` | HuggingFace-style ID advertised via `/v1/models` (default: `CUSTOM_BACKEND_MODEL`, or the literal `custom` if that is unset too) |

## Where the model weights come from

Model weights are baked into the `parakeet` image, so there's nothing to download on first start. Unmodified copies of those weights are kept at [huggingface.co/DictionLabs](https://huggingface.co/DictionLabs) ([Parakeet v3 ONNX](https://huggingface.co/DictionLabs/parakeet-tdt-0.6b-v3-onnx), [diarization](https://huggingface.co/DictionLabs/Nemotron-3-Diarization-ONNX), [Silero VAD](https://huggingface.co/DictionLabs/silero-vad-onnx)). `DEFAULT_MODEL: parakeet-v3` covers 25 European languages - for the other 74, see [Whisper on CPU](speech-models.md#whisper-on-cpu-99-languages) or, with an NVIDIA GPU, [large-v3-turbo](speech-models.md#nvidia-gpu-whisper-large-v3-turbo).
