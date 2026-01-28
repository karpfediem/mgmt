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
}:
buildGoModule rec {
  pname = "mgmt";
  version = "1.0.1-master";

  src = ./.;

  postPatch = ''
    patchShebangs misc/header.sh
  '';
  preBuild = ''
    make lang resources funcgen
  '';
  overrideModAttrs = _: {
    preBuild = "";
  };

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
  ];

  ldflags = [
    "-s"
    "-w"
    "-X main.program=${pname}"
    "-X main.version=${version}"
  ];

  subPackages = [ "." ];


  vendorHash = "sha256-c8jVgbfLPIbfJ9Qr6ux6QAPvYrJpCpVESnEpr5gVgTE=";

  meta = with lib; {
    description = "Next generation distributed, event-driven, parallel config management";
    homepage = "https://mgmtconfig.com";
    license = licenses.gpl3Only;
    maintainers = with maintainers; [ urandom ];
    mainProgram = "mgmt";
  };
}
