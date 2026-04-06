{ augeas
, buildGoModule
, fetchFromGitHub
, gotools
, lib
, libvirt
, libxml2
, nex
, pkg-config
, ragel
,
}:
buildGoModule rec {
  pname = "mgmt";
  version = "1.0.1-master";

  src = ./.;
  # src = fetchFromGitHub { owner = "purpleidea"; repo = "mgmt"; rev = "c7ab7de9acdcb8a129638d351a2a3c4d75f8ce23"; sha256 = "sha256-A5iwaOB9t5vmTt93AdPEoh0+61dMngp3uCopVp/0Ig8="; };

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


  vendorHash = "sha256-9ImGmgpdAQOiOJ89bnRAkkajKQKvWKGvZ3vgYxSbk8w=";

  meta = with lib; {
    description = "Next generation distributed, event-driven, parallel config management";
    homepage = "https://mgmtconfig.com";
    license = licenses.gpl3Only;
    maintainers = with maintainers; [ urandom ];
    mainProgram = "mgmt";
  };
}
