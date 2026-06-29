{
  description = "mgmt";

  inputs = {
    flake-parts.url = "github:hercules-ci/flake-parts";
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = inputs @ {self, flake-parts, ...}:
    flake-parts.lib.mkFlake {inherit inputs;} {
      imports = [
      ];

      systems = ["x86_64-linux" "aarch64-linux" "aarch64-darwin" "x86_64-darwin"];

      perSystem = {pkgs, ...}: let
        date = builtins.substring 0 8 (self.lastModifiedDate or "19700101");
        rev = self.shortRev or self.dirtyShortRev or "unknown";
        mgmt = pkgs.callPackage ./package.nix {
          src = ./.;
          version = "0-unstable-${date}-${rev}";
        };
      in {
        packages = {
          default = mgmt;
          mgmt = mgmt;
        };
      };

      flake = {
      };
    };
}
