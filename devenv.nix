{ pkgs, lib, config, inputs, ... }:
let
  pkgs-unstable = import inputs.nixpkgs-unstable { system = pkgs.stdenv.system; };
in
{
  packages = with pkgs; [
    gnumake
    golint
    mdl
    gdb
    pkgs-unstable.delve
    pkgs-unstable.etcd
    pkg-config
    libvirt
    libxml2
    augeas
    nex
    ragel
    which
    graphviz
    graphviz-nox
    gcc
    bash
    inotify-tools
  ];
  languages.go = {
    enable = true;
    package = pkgs-unstable.go;
  };

  env.PKG_CONFIG_PATH = with pkgs; lib.concatStringsSep ":" [
    "${libxml2.dev}/lib/pkgconfig"
    "${libxml2.dev}/share/pkgconfig"
    "${augeas.dev}/lib/pkgconfig"
    "${augeas.dev}/share/pkgconfig"
    "${libvirt}/lib/pkgconfig"
    "${libvirt}/share/pkgconfig"
  ];
  env.LD_LIBRARY_PATH = with pkgs; lib.makeLibraryPath [ libxml2 augeas libvirt ];


  shell = lib.mkForce (pkgs.buildFHSEnv {
    name = "devenv-shell";
    targetPkgs = _: config.packages;
    runScript = "bash";
    profile = ''
      ${lib.optionalString config.devenv.debug "set -x"}
      ${config.enterShell}
    '' + lib.concatStringsSep "\n" (lib.mapAttrsToList (name: value: ''
      export ${name}=${value}
    '') config.env);
  }).env;
}
