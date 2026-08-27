{
  buildGoModule,
  lib,
}:

buildGoModule rec {
  pname = "codex-usage-dashboard";
  version = "0.1.0";

  src = lib.cleanSource ./.;
  vendorHash = null;

  subPackages = [ "cmd/codex-usage-dashboard" ];
  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
  ];

  doCheck = true;

  meta = {
    description = "Tailnet-only dashboard for isolated local Codex account quotas";
    mainProgram = "codex-usage-dashboard";
    platforms = lib.platforms.linux;
  };
}
