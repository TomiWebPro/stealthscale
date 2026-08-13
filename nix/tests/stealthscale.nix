{ pkgs, lib, ... }:
let
  tls-cert = pkgs.runCommand "selfSignedCerts" { buildInputs = [ pkgs.openssl ]; } ''
    openssl req \
      -x509 -newkey rsa:4096 -sha256 -days 365 \
      -nodes -out cert.pem -keyout key.pem \
      -subj '/CN=stealthscale' -addext "subjectAltName=DNS:stealthscale"

    mkdir -p $out
    cp key.pem cert.pem $out
  '';
in
{
  name = "stealthscale";
  meta.maintainers = with lib.maintainers; [
    kradalby
    misterio77
  ];

  nodes =
    let
      stealthscalePort = 8080;
      stunPort = 3478;
      peer = {
        services.tailscale.enable = true;
        security.pki.certificateFiles = [ "${tls-cert}/cert.pem" ];
      };
    in
    {
      peer1 = peer;
      peer2 = peer;

      stealthscale = {
        services = {
          stealthscale = {
            enable = true;
            port = stealthscalePort;
            settings = {
              server_url = "https://stealthscale";
              ip_prefixes = [ "100.64.0.0/10" ];
              derp = {
                server = {
                  enabled = true;
                  region_id = 999;
                  stun_listen_addr = "0.0.0.0:${toString stunPort}";
                };
                urls = [ ];
              };
              dns = {
                base_domain = "tailnet";
                extra_records = [
                  {
                    name = "foo.bar";
                    type = "A";
                    value = "100.64.0.2";
                  }
                ];
                override_local_dns = false;
              };
            };
          };
          nginx = {
            enable = true;
            virtualHosts.stealthscale = {
              addSSL = true;
              sslCertificate = "${tls-cert}/cert.pem";
              sslCertificateKey = "${tls-cert}/key.pem";
              locations."/" = {
                proxyPass = "http://127.0.0.1:${toString stealthscalePort}";
                proxyWebsockets = true;
              };
            };
          };
        };
        networking.firewall = {
          allowedTCPPorts = [
            80
            443
          ];
          allowedUDPPorts = [ stunPort ];
        };
        environment.systemPackages = [ pkgs.stealthscale ];
      };
    };

  testScript = ''
    start_all()
    stealthscale.wait_for_unit("stealthscale")
    stealthscale.wait_for_open_port(443)

    # Create stealthscale user and preauth-key
    stealthscale.succeed("stealthscale users create test")
    authkey = stealthscale.succeed("stealthscale preauthkeys -u 1 create --reusable")

    # Connect peers
    up_cmd = f"tailscale up --login-server 'https://stealthscale' --auth-key {authkey}"
    peer1.execute(up_cmd)
    peer2.execute(up_cmd)

    # Check that they are reachable from the tailnet
    peer1.wait_until_succeeds("tailscale ping peer2")
    peer2.wait_until_succeeds("tailscale ping peer1.tailnet")
    assert (res := peer1.wait_until_succeeds("${lib.getExe pkgs.dig} +short foo.bar").strip()) == "100.64.0.2", f"Domain {res} did not match 100.64.0.2"
  '';
}
