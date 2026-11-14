# Pairing and Access Control

The gateway generates its own pairing key on first start and prints it as a QR code in its log.
Scanning it in the app (Self-Hosted → **Scan to Pair**) sets the server address and key in one step.

| Variable | Default | Description |
|----------|---------|-------------|
| `PUBLIC_URL` | empty | The address the QR code points the app at, e.g. `http://192.168.1.42:8080` or `https://diction.example.com`. Scheme, host and port only. Without it, the QR carries only the key and you type the URL yourself. |
| `DICTION_GATEWAY_AUTH` | `optional` | `optional`: the key is accepted, but requests without one still pass. `required`: requests without a valid key get `401`. `off`: no key, no QR. |
| `DICTION_TOKEN_TTL` | unset (never expires) | Lifetime of each paired device's key, e.g. `2160h` (90 days). Only has an effect with `required`. The app renews its key well before expiry. Turning it on for an existing setup makes every device pair once more. |

The default is `optional` so that upgrading the image never locks an existing setup out. **On any
host reachable from the internet, set `DICTION_GATEWAY_AUTH: required`**, and consider a
`DICTION_TOKEN_TTL`, because the QR code in `docker logs` is a working key.

```yaml
  gateway:
    environment:
      DEFAULT_MODEL: parakeet-v3
      PUBLIC_URL: "https://diction.example.com"
      DICTION_GATEWAY_AUTH: required
      DICTION_TOKEN_TTL: 2160h
    volumes:
      - gateway-data:/data
```

The key lives in `/data`; keep the `gateway-data` volume from the setup compose file or every rebuild issues a new
key and every phone has to scan again. `docker exec diction-gateway gateway auth` prints a fresh
QR at any time, and each scan gets its own key. Required mode also opens the
[AI text routes](ai-cleanup.md) to paired devices, so `TEXT_ROUTES_OPEN` is
not needed with it. Full details, including key rotation: [`AGENTS.md`](../AGENTS.md).

An older gateway setting, `AUTH_ENABLED=true`, is retired. If you still have it set, the gateway
treats it as `DICTION_GATEWAY_AUTH: required` and prints a warning; replace it with that line.
