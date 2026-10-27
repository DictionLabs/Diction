{
  config,
  lib,
  pkgs,
  ...
}: let
  cfg = config.services.diction-gateway;
  inherit (lib) mkEnableOption mkOption mkIf types optionalAttrs;
in {
  imports = [
    (lib.mkRemovedOptionModule ["services" "diction-gateway" "authEnabled"] ''
      `authEnabled` (AUTH_ENABLED) switched on Diction Cloud's App Store receipt checks and
      locked self-hosters out. Use `services.diction-gateway.gatewayAuth = "required";`
      and pair the app by scanning the gateway's QR code.
    '')
  ];

  options.services.diction-gateway = {
    enable = mkEnableOption "Diction self-hosted STT gateway";

    package = mkOption {
      type = types.package;
      default = pkgs.diction-gateway or (pkgs.callPackage ./package.nix {});
      defaultText = lib.literalExpression "pkgs.diction-gateway";
      description = "The diction-gateway package to use.";
    };

    port = mkOption {
      type = types.port;
      default = 8080;
      description = ''
        TCP port the gateway listens on. The gateway always binds to all
        interfaces; restrict access with the firewall.
      '';
    };

    openFirewall = mkOption {
      type = types.bool;
      default = false;
      description = "Whether to open `port` in the host firewall.";
    };

    defaultModel = mkOption {
      type = types.str;
      default = "small";
      example = "large-v3-turbo";
      description = ''
        Value of the `DEFAULT_MODEL` env var. Valid built-in values:
        `small`, `medium`, `large-v3-turbo`, `distil-large-v3`,
        `parakeet-v3`, `canary-v2`, `canary-qwen`. When
        `customBackend.url` is set the gateway routes to the custom
        backend regardless, but this still appears in `/v1/models` listings.
      '';
    };

    maxBodySize = mkOption {
      type = types.ints.positive;
      default = 209715200;
      description = "Max request body size in bytes (`MAX_BODY_SIZE`). Default 200 MiB.";
    };

    customBackend = {
      url = mkOption {
        type = types.nullOr types.str;
        default = null;
        example = "http://127.0.0.1:8000";
        description = ''
          Optional OpenAI-compatible Whisper backend the gateway forwards
          audio to. Sets `CUSTOM_BACKEND_URL`. When set, the custom
          backend becomes the default.
        '';
      };
      model = mkOption {
        type = types.nullOr types.str;
        default = null;
        example = "Systran/faster-whisper-large-v3-turbo";
        description = "Model name forwarded to the backend (`CUSTOM_BACKEND_MODEL`).";
      };
      needsWav = mkOption {
        type = types.bool;
        default = false;
        description = ''
          Set if the backend only accepts WAV input. When true the gateway
          transcodes to 16 kHz mono WAV via ffmpeg
          (`CUSTOM_BACKEND_NEEDS_WAV`).
        '';
      };
      authHeader = mkOption {
        type = types.nullOr types.str;
        default = null;
        example = "Bearer sk-xxx";
        description = ''
          Authorization header value forwarded to the backend
          (`CUSTOM_BACKEND_AUTH`). For secrets prefer
          `environmentFile` instead of putting the value in the Nix store.
        '';
      };
    };

    llm = {
      baseUrl = mkOption {
        type = types.nullOr types.str;
        default = null;
        example = "http://127.0.0.1:11434/v1";
        description = ''
          Optional OpenAI-compatible LLM endpoint for transcript
          post-processing (`LLM_BASE_URL`). When set together with
          `model`, transcripts are passed through the LLM before being
          returned.
        '';
      };
      model = mkOption {
        type = types.nullOr types.str;
        default = null;
        example = "gemma2:9b";
        description = "Model name for LLM post-processing (`LLM_MODEL`).";
      };
      apiKey = mkOption {
        type = types.nullOr types.str;
        default = null;
        description = ''
          API key for the LLM provider (`LLM_API_KEY`). Prefer
          `environmentFile` for real secrets.
        '';
      };
      prompt = mkOption {
        type = types.nullOr types.str;
        default = null;
        description = ''
          System prompt for the LLM, or a path to a file containing it
          (`LLM_PROMPT`).
        '';
      };
    };

    environmentFile = mkOption {
      type = types.nullOr types.path;
      default = null;
      example = "/run/secrets/diction-gateway.env";
      description = ''
        Path to an `EnvironmentFile=` for systemd. Use this to inject
        secrets such as `CUSTOM_BACKEND_AUTH` or `LLM_API_KEY` without
        writing them to the Nix store.
      '';
    };

    gatewayAuth = mkOption {
      type = types.enum ["off" "optional" "required"];
      default = "optional";
      description = ''
        Pairing key enforcement (`DICTION_GATEWAY_AUTH`). `optional` accepts the
        key but lets keyless requests through; `required` rejects them with 401.
        Use `required` whenever the gateway is reachable from the internet.
        The pairing QR is printed to the journal at start
        (`journalctl -u diction-gateway`); to print a fresh one, run
        `sudo DICTION_KEY_PATH=/var/lib/diction-gateway/gateway-keys.json diction-gateway auth`.
      '';
    };

    publicUrl = mkOption {
      type = types.nullOr types.str;
      default = null;
      example = "https://diction.example.com";
      description = ''
        Address embedded in the pairing QR code (`PUBLIC_URL`), scheme, host
        and port only. Without it the QR carries only the key.
      '';
    };

    extraEnvironment = mkOption {
      type = types.attrsOf types.str;
      default = {};
      example = lib.literalExpression ''{ SOME_VAR = "value"; }'';
      description = "Extra environment variables passed to the gateway.";
    };
  };

  config = mkIf cfg.enable {
    systemd.services.diction-gateway = {
      description = "Diction self-hosted STT gateway";
      wantedBy = ["multi-user.target"];
      after = ["network-online.target"];
      wants = ["network-online.target"];

      environment =
        {
          GATEWAY_PORT = toString cfg.port;
          DEFAULT_MODEL = cfg.defaultModel;
          MAX_BODY_SIZE = toString cfg.maxBodySize;
          DICTION_GATEWAY_AUTH = cfg.gatewayAuth;
          # The pairing keyring must survive restarts, or every paired phone re-scans.
          DICTION_KEY_PATH = "/var/lib/diction-gateway/gateway-keys.json";
        }
        // optionalAttrs (cfg.publicUrl != null) {
          PUBLIC_URL = cfg.publicUrl;
        }
        // optionalAttrs (cfg.customBackend.url != null) {
          CUSTOM_BACKEND_URL = cfg.customBackend.url;
        }
        // optionalAttrs (cfg.customBackend.model != null) {
          CUSTOM_BACKEND_MODEL = cfg.customBackend.model;
        }
        // optionalAttrs cfg.customBackend.needsWav {
          CUSTOM_BACKEND_NEEDS_WAV = "true";
        }
        // optionalAttrs (cfg.customBackend.authHeader != null) {
          CUSTOM_BACKEND_AUTH = cfg.customBackend.authHeader;
        }
        // optionalAttrs (cfg.llm.baseUrl != null) {
          LLM_BASE_URL = cfg.llm.baseUrl;
        }
        // optionalAttrs (cfg.llm.model != null) {
          LLM_MODEL = cfg.llm.model;
        }
        // optionalAttrs (cfg.llm.apiKey != null) {
          LLM_API_KEY = cfg.llm.apiKey;
        }
        // optionalAttrs (cfg.llm.prompt != null) {
          LLM_PROMPT = cfg.llm.prompt;
        }
        // cfg.extraEnvironment;

      serviceConfig =
        {
          ExecStart = lib.getExe cfg.package;
          Restart = "on-failure";
          RestartSec = "5s";

          DynamicUser = true;
          StateDirectory = "diction-gateway";

          NoNewPrivileges = true;
          ProtectSystem = "strict";
          ProtectHome = true;
          ProtectKernelTunables = true;
          ProtectKernelModules = true;
          ProtectControlGroups = true;
          ProtectClock = true;
          ProtectHostname = true;
          ProtectKernelLogs = true;
          ProtectProc = "invisible";
          PrivateTmp = true;
          PrivateDevices = true;
          RestrictNamespaces = true;
          RestrictRealtime = true;
          RestrictSUIDSGID = true;
          LockPersonality = true;
          MemoryDenyWriteExecute = true;
          RestrictAddressFamilies = ["AF_INET" "AF_INET6" "AF_UNIX"];
          SystemCallArchitectures = "native";
          SystemCallFilter = ["@system-service" "~@privileged"];
          CapabilityBoundingSet = [];
          AmbientCapabilities = [];
        }
        // optionalAttrs (cfg.environmentFile != null) {
          EnvironmentFile = cfg.environmentFile;
        };
    };

    networking.firewall.allowedTCPPorts = mkIf cfg.openFirewall [cfg.port];
  };
}
