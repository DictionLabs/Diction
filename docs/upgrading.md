# Upgrading

```bash
docker compose pull && docker compose up -d
```

Containers are recreated; the named volumes (`gateway-data` with the pairing key, `whisper-models`
with downloaded weights) are kept, so phones stay paired and models are not downloaded again.

Every gateway release is also published under its version tag. To pin one, or roll back after a
bad upgrade, replace `latest` with a tag from the
[releases page](https://github.com/DictionLabs/Diction/releases), e.g.
`image: dictionlabs/gateway:v14.1`, and run `docker compose up -d`.

## Moving off the old image name

> **`ghcr.io/omachala/diction-gateway` is retired as of v13.0 (2026-09-11).** Images already
> published there stay pullable and are not going anywhere, but **no new versions will land on it** -
> that ref is frozen at v12.0. If you are pulling it, switch to `dictionlabs/gateway` to keep
> receiving updates:
>
> ```diff
> -    image: ghcr.io/omachala/diction-gateway:latest
> +    image: dictionlabs/gateway:latest
> ```
>
> Images are published under [Diction Labs](https://github.com/DictionLabs) on Docker Hub
> (`dictionlabs/gateway`, canonical) and GHCR (`ghcr.io/dictionlabs/gateway`).
