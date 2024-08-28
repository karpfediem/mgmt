{
  augeas,
  buildGoModule,
  fetchFromGitHub,
  gotools,
  lib,
  libvirt,
  libxml2,
  nex,
  pkg-config,
  ragel,
  which,
}:
buildGoModule rec {
  pname = "mgmt";
  version = "0.0.26-master";

  src = fetchFromGitHub {
    owner = "purpleidea";
    repo = pname;
    rev = "380004b1dccb1c76c2d87c5473c4dc105c8a6319";
    hash = "sha256-+LQaqCM2UUBLCkCCJ30KQTA1VYEyX6zym/dp415IIVA=";
  };

  # patching must be done in prebuild, so it is shared with goModules
  # see https://github.com/NixOS/nixpkgs/issues/208036
  # regarding osutil: https://github.com/tredoe/osutil/issues/15 and others, somehow deleted lots of tags...
  # current mgmt master uses v1.5.0, can replace this patch if there is a new mgmt release after 0.0.26
  preBuild = ''
    for file in `find -name Makefile -type f`; do
      substituteInPlace $file --replace-quiet "/usr/bin/env " ""
    done

    patchShebangs misc/header.sh
    make lang funcgen
  '';

  buildInputs = [
    augeas
    libvirt
    libxml2
  ];

  nativeBuildInputs = [
    gotools
    nex
    pkg-config
    ragel
    which
  ];

  ldflags = [
    "-s"
    "-w"
    "-X main.program=${pname}"
    "-X main.version=${version}"
  ];

  subPackages = ["."];

  vendorHash = "sha256-/CimTvGwIowOdti9+pg2rQV9eEhhJ/zpVeZSlvUd3uE=";

  meta = with lib; {
    description = "Next generation distributed, event-driven, parallel config management!";
    homepage = "https://mgmtconfig.com";
    license = licenses.gpl3Only;
    maintainers = with maintainers; [urandom];
    mainProgram = "mgmt";
  };
}
