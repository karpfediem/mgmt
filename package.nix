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
, enableAugeas ? true
, enableDocker ? true
, enableVirt ? true
, enableCgo ? (enableAugeas || enableVirt)
,
}:
let
  disabledTags =
    lib.optionals (!enableAugeas) [ "noaugeas" ]
    ++ lib.optionals (!enableVirt) [ "novirt" ]
    ++ lib.optionals (!enableDocker) [ "nodocker" ];
in
assert lib.assertMsg (enableCgo || !(enableAugeas || enableVirt))
  "mgmt package cannot disable CGO while Augeas or libvirt support remains enabled";
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

  buildInputs =
    lib.optionals enableAugeas [
      augeas
      libxml2
    ]
    ++ lib.optionals enableVirt [
      libvirt
    ];

  nativeBuildInputs = [
    gotools
    nex
    ragel
  ]
  ++ lib.optionals enableCgo [
    pkg-config
  ];

  env.CGO_ENABLED = if enableCgo then 1 else 0;
  tags = disabledTags;

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
