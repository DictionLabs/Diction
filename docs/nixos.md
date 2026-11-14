# NixOS

The repo ships a flake with a hardened systemd module - no Docker needed.

```bash
nix run github:DictionLabs/Diction#diction-gateway
```

Enable as a service:

```nix
{
  inputs.diction.url = "github:DictionLabs/Diction";

  outputs = { nixpkgs, diction, ... }: {
    nixosConfigurations.your-host = nixpkgs.lib.nixosSystem {
      modules = [
        diction.nixosModules.default
        {
          services.diction-gateway = {
            enable = true;
            openFirewall = true;
            # gatewayAuth = "required";   # pairing key on every request
            # publicUrl = "https://diction.example.com";
            # customBackend.url = "http://127.0.0.1:8000";
            # llm.baseUrl = "http://127.0.0.1:11434/v1";
            # llm.model = "gemma2:9b";
            # environmentFile = "/run/secrets/diction-gateway.env";
          };
        }
      ];
    };
  };
}
```

The unit runs under `DynamicUser` with `ProtectSystem=strict`, `NoNewPrivileges`, and a narrow syscall filter. The pairing key is kept in `/var/lib/diction-gateway`, and the pairing QR is in `journalctl -u diction-gateway`. Use `environmentFile` for secrets - they don't end up in the world-readable Nix store. Full option list: [`nix/module.nix`](../nix/module.nix).
