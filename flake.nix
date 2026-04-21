{
  description = "mgmt";

  inputs = {
    flake-parts.url = "github:hercules-ci/flake-parts";
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = inputs @ {flake-parts, ...}:
    flake-parts.lib.mkFlake {inherit inputs;} {
      imports = [
      ];

      systems = ["x86_64-linux" "aarch64-linux" "aarch64-darwin" "x86_64-darwin"];

      perSystem = {pkgs, ...}: let
        mgmt = pkgs.callPackage ./package.nix {};
        mgmt-minimal = pkgs.callPackage ./package.nix {
          enableAugeas = false;
          enableDocker = false;
          enableVirt = false;
          enableCgo = false;
        };
        deploy-readfile-change-vm = pkgs.callPackage ./test/nixos/deploy-readfile-change.nix {
          mgmt = mgmt-minimal;
        };
      in {
        packages.default = mgmt;
        packages.minimal = mgmt-minimal;
        checks.deploy-readfile-change-vm = deploy-readfile-change-vm;
      };

      flake = {
      };
    };
}
