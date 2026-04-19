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


  vendorHash = "sha256-CsLVU3tNXDv3/5Ok0HFrXc3+2i9YtK4KaYeTIDScN64=";

  meta = with lib; {
    description = "Next generation distributed, event-driven, parallel config management";
    homepage = "https://mgmtconfig.com";
    license = licenses.gpl3Only;
    maintainers = with maintainers; [ urandom ];
    mainProgram = "mgmt";
  };
}
