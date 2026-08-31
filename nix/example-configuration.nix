# Example NixOS configuration using the coordination module
#
# This file demonstrates how to use the coordination NixOS module from this flake.
# To use in your own configuration, add this to your flake.nix inputs:
#
#   inputs.stealthscale.url = "github:juanfont/stealthscale";
#
# Then import the module:
#
#   imports = [ inputs.stealthscale.nixosModules.default ];
#

{ config, pkgs, ... }:

{
  # Import the coordination module
  # In a real configuration, this would come from the flake input
  # imports = [ inputs.stealthscale.nixosModules.default ];

  services.stealthscale = {
    enable = true;

    # Optional: Use a specific package (defaults to pkgs.stealthscale)
    # package = pkgs.stealthscale;

    # Listen on all interfaces (default is 127.0.0.1)
    address = "0.0.0.0";
    port = 8080;

    settings = {
      # The URL clients will connect to
      server_url = "https://coord.example.com";

      # IP prefixes for the tailnet
      # These use the freeform settings - you can set any coordination config option
      prefixes = {
        v4 = "100.64.0.0/10";
        v6 = "fd7a:115c:a1e0::/48";
        allocation = "sequential";
      };

      # DNS configuration with MagicDNS
      dns = {
        magic_dns = true;
        base_domain = "tailnet.example.com";

        # Whether to override client's local DNS settings (default: true)
        # When true, nameservers.global must be set
        override_local_dns = true;

        nameservers = {
          global = [ "1.1.1.1" "8.8.8.8" ];
        };
      };

      # DERP (relay) configuration
      derp = {
        # No public DERP servers are configured by default. The embedded DERP
        # server (derp.server.enabled) is only used as a stealth-gated fallback
        # and must not be exposed directly.
        urls = [ ];
        auto_update_enabled = true;
        update_frequency = "24h";

        # Optional: Run your own DERP server
        # server = {
        #   enabled = true;
        #   region_id = 999;
        #   stun_listen_addr = "0.0.0.0:3478";
        # };
      };

      # Database configuration (SQLite is recommended)
      database = {
        type = "sqlite";
        sqlite = {
          path = "/var/lib/coordination/db.sqlite";
          write_ahead_log = true;
        };

        # PostgreSQL example (not recommended for new deployments)
        # type = "postgres";
        # postgres = {
        #   host = "localhost";
        #   port = 5432;
        #   name = "coordination";
        #   user = "coordination";
        #   password_file = "/run/secrets/coordination-db-password";
        # };
      };

      # Logging configuration
      log = {
        level = "info";
        format = "text";
      };

      # Optional: OIDC authentication
      # oidc = {
      #   issuer = "https://accounts.google.com";
      #   client_id = "your-client-id";
      #   client_secret_path = "/run/secrets/oidc-client-secret";
      #   scope = [ "openid" "profile" "email" ];
      #   allowed_domains = [ "example.com" ];
      # };

      # Optional: Let's Encrypt TLS certificates
      # tls_letsencrypt_hostname = "coord.example.com";
      # tls_letsencrypt_challenge_type = "HTTP-01";

      # Optional: Provide your own TLS certificates
      # tls_cert_path = "/path/to/cert.pem";
      # tls_key_path = "/path/to/key.pem";

      # ACL policy configuration
      policy = {
        mode = "file";
        path = "/var/lib/coordination/policy.hujson";
      };

      # Stealth transport (VLESS + Reality + uTLS) — defaults are production-ready
      # The per-server secret is auto-persisted to .xray_secret for sqlite;
      # for postgres you MUST set settings.xray.secret (openssl rand -hex 32)
      # or the server will fail to start and vless:// URIs will be unstable.
      xray = {
        enabled = true;
        security = "reality_xtls";
        # secret = "your-64-hex-secret-for-postgres";
        listen_addr = "0.0.0.0";
        listen_port = 10001;
        max_listen_port = 10100;
        utls_fingerprint = "chrome";
        reality = {
          dest = "www.cloudflare.com:443";
          server_names = [ "www.cloudflare.com" "www.microsoft.com" "cloudflare.com" "microsoft.com" ];
          # short_ids = [ "your-short-id" ]; # auto from secret if empty
          spider_x = "/";
        };
        stealth = {
          enforce = true;
          enforce_control = true; # hide /ts2021, gate WebUI behind API key
          probe_interval = "30s";
          probe_timeout = "5s";
        };
      };

      # You can add ANY stealthscale configuration option here thanks to freeform settings
      # For example, experimental features or settings not explicitly defined above:
      # experimental_feature = true;
      # custom_setting = "value";
    };
  };

  # Optional: Open firewall ports
  networking.firewall = {
    allowedTCPPorts = [ 8080 ];
    # If running a DERP server:
    # allowedUDPPorts = [ 3478 ];
  };

  # Optional: Use with nginx reverse proxy for TLS termination
  # services.nginx = {
  #   enable = true;
  #   virtualHosts."coord.example.com" = {
  #     enableACME = true;
  #     forceSSL = true;
  #     locations."/" = {
  #       proxyPass = "http://127.0.0.1:8080";
  #       proxyWebsockets = true;
  #     };
  #   };
  # };
}
