# This file defines a Nix package for stealthscale.
# Ultimately, this should go to nixpkgs.

{ lib, pkgs, ... }:

let
  # This is a workaround to avoid infinite recursion due to the crane
  # tarball having a nested folder.
  fetchTreeWithSub = { url, rev, sub }: pkgs.fetchTree {
    inherit url rev;
    # Strip the top-level directory and use the subdirectory instead.
    # Note: this expects the tarball to have exactly one top-level directory.
    # The subdirectory must exist in the tarball.
    postFetch = (prev: prev // {
      # Unpack the tarball to a temporary location
      unpacked = pkgs.fetchTarball {
        url = url;
        rev = rev;
        sha256 = prev.sha256;
      };
      # Move the subdirectory to the expected location
      # and remove the temporary directory.
      output = lib.optionalString (builtins.pathExists "${pkgs.sourceRoot}/${sub}")
        (pkgs.lib.forceSerialize {
          type = "symlink";
          target = "${pkgs.sourceRoot}/${sub}";
        });
    });
  };

  # Fetch the sources from GitHub using the current Git revision
  # or a tag if one exists.
  src = pkgs.fetchFromGitHub {
    owner = "tomiwebpro";
    repo = "stealthscale";
    # Using `rev` instead of `tag` here ensures we get the exact commit.
    # If you're building from a tag, you'll want to use `tag` instead.
    rev = pkgs.lib.getRev "main";
    # If you're building from a tag, uncomment the following line:
    # tag = "v0.1.0";
    sha256 = "0000000000000000000000000000000000000000000000000000";
  };

  # Get the short revision for versioning
  shortRev = pkgs.lib.gitRevLen pkgs.lib.getRev "main" 7;
  dirtyShortRev = if pkgs.lib.isDirtySource then "${shortRev}-dirty" else shortRev;
  # Use the short revision if we don't have a tag, otherwise use the tag version.
  version = "0.0.0-${dirtyShortRev}";

in rec {
  pname = "stealthscale";
  version = "0.0.0-${dirtyShortRev}";

  passthru = {
    revision = builtins.substring 0 7 (builtins.replaceStrings [ "-" ] [ "" ] version);
  };

  # Build the Go binary
  buildGo = { goVersion, subPackages ? [], mainProgram ? "", ... }:
    let
      go = pkgs.go.packages.${goVersion}.override {
        withStdlib = false;
      };
    in
      go.buildGoPackage {
        pname = pname;
        version = version;
        src = src;
        buildGoPackage = go.buildGoPackage;
        goPackagePath = pname;
        subPackages = subPackages;
        # For binaries, we need to specify the main package
        # This is a workaround for older versions of nixpkgs
        # In newer versions, you can use `mainPackage`
        makeCPPFlags = [];
        # Disable CGO for static builds if needed
        # CGO_ENABLED = 0;
        # Don't strip the binary to preserve debug symbols
        # dontStrip = true;
        # Add build flags
        goLDFlags = "-X main.version=${version}";
        # Install the binaries
        installPhase = ''
          mkdir -p $out/bin
          cp ${if mainProgram != "" then "./${mainProgram}" else "./${pname}"} $out/bin/
        '';
      };

  # Package stealthscale as a Nix package.
  stealthscale = buildGo {
    pname = "stealthscale";
    version = "0.0.0-${dirtyShortRev}";
    subPackages = [ "cmd/stealthscale" ];
    mainProgram = "stealthscale";
  };

  # Alias the package as `stealthscale` (the main program name).
  default = stealthscale;

  # Add entry to build a docker image with stealthscale
  # nix build .#stealthscale-docker
  stealthscale-docker = pkgs.dockerTools.buildLayeredImage {
    name = "stealthscale";
    tag = version;
    contents = [ stealthscale ];
    config.Entrypoint = [ "/bin/sh" ];
    config.Cmd = [ "${stealthscale}/bin/stealthscale" ];
  };
}