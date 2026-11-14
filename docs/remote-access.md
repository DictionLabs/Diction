# Use your server away from home, safely

By default your phone only finds the server on your home Wi-Fi. These are three ways to use it from anywhere.

**Tailscale (recommended)**

[Tailscale](https://tailscale.com/) creates a private WireGuard mesh between your devices. Install it on the server and iPhone, sign in to the same account, and use the `100.x.x.x` Tailscale IP as your Diction endpoint. Works on cellular, café WiFi, anywhere. Free for personal use. Nothing is exposed to the internet, so pairing can stay optional.

**Cloudflare Tunnel (public URL, no port forwarding)**

Add to your compose file:

```yaml
  cloudflared:
    image: cloudflare/cloudflared:latest
    container_name: diction-cloudflared
    restart: unless-stopped
    command: tunnel --no-autoupdate run
    environment:
      TUNNEL_TOKEN: "${CLOUDFLARE_TUNNEL_TOKEN}"
```

Create a tunnel in the [Cloudflare Zero Trust dashboard](https://one.dash.cloudflare.com/), grab the token, add it to `.env`, route the public hostname to `http://gateway:8080`. Free tier. The gateway is now public: turn on [required pairing](pairing.md). Note: transcripts pass through Cloudflare's network (HTTPS-encrypted, but a third party is in the path).

**ngrok (quick testing)**

```bash
ngrok http 8080
```

Free tier URLs change on restart - good for a demo, not daily use.
