{
  buildGoModule,
  lib,
  nodejs,
}:

buildGoModule rec {
  pname = "codex-usage-dashboard";
  version = "0.1.0";

  src = lib.cleanSource ./.;
  vendorHash = null;

  subPackages = [ "cmd/codex-usage-dashboard" ];
  nativeCheckInputs = [ nodejs ];
  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
  ];

  doCheck = true;
  checkPhase = ''
    runHook preCheck
    go test ./...
    node --check internal/web/static/assets/account_logic.js
    node --check internal/web/static/assets/app.js
    node --test internal/web/account_logic_test.cjs
    runHook postCheck
  '';

  meta = {
    description = "Tailnet-only dashboard for isolated local Codex account quotas";
    mainProgram = "codex-usage-dashboard";
    platforms = lib.platforms.linux;
  };
}
