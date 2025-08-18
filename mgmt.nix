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
  pname = "mgmt";
  version = "0.0.27";
in
buildGoModule {
  inherit pname;
  inherit version;

  src = ./.;

  postPatch = ''
    patchShebangs misc/header.sh
  '';
  preBuild = ''
    make
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

  vendorHash = "sha256-kSEcK4VWuO8FbXr4caH1LLe9O8wdRHs+Csfn85N7+Uc=";

  meta = with lib; {
    description = "Next generation distributed, event-driven, parallel config management!";
    homepage = "https://mgmtconfig.com";
    license = licenses.gpl3Only;
    maintainers = with maintainers; [ urandom ];
    mainProgram = "mgmt";
  };
}
