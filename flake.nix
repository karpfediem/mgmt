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
        deploy-svc-restart-change-vm = pkgs.callPackage ./test/nixos/deploy-svc-restart-change.nix {
          mgmt = mgmt-minimal;
        };
        deploy-svc-reload-change-vm = pkgs.callPackage ./test/nixos/deploy-svc-reload-change.nix {
          mgmt = mgmt-minimal;
        };
        deploy-svc-notify-change-vm = pkgs.callPackage ./test/nixos/deploy-svc-notify-change.nix {
          mgmt = mgmt-minimal;
        };
      in {
        packages.default = mgmt;
        packages.minimal = mgmt-minimal;
        checks.deploy-readfile-change-vm = deploy-readfile-change-vm;
        checks.deploy-svc-restart-change-vm = deploy-svc-restart-change-vm;
        checks.deploy-svc-reload-change-vm = deploy-svc-reload-change-vm;
        checks.deploy-svc-notify-change-vm = deploy-svc-notify-change-vm;
      };

      flake = {
      };
    };
}
