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
let
  version = "0.0.27";
in
buildGoModule rec {
  pname = "mgmt";
  inherit version;

  src = ./.;

  postPatch = ''
    patchShebangs misc/header.sh
  '';
  preBuild = ''
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

  subPackages = [ "." ];

  vendorHash = "sha256-VRMf9b8OSAsjm5xNz5s0hMpiphBctPKMZcFT/Rzonto=";

  meta = with lib; {
    description = "Next generation distributed, event-driven, parallel config management!";
    homepage = "https://mgmtconfig.com";
    license = licenses.gpl3Only;
    maintainers = with maintainers; [ urandom ];
    mainProgram = "mgmt";
  };
}
