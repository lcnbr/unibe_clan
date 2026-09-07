{
  lib,
  stdenvNoCC,
  fetchurl,
}: let
  version = "0.153.4";
  target = "x86_64-unknown-linux-musl";
in
  stdenvNoCC.mkDerivation {
    pname = "codex";
    inherit version;

    src = fetchurl {
      url = "https://github.com/openai/codex/releases/download/rust-v${version}/codex-${target}.tar.gz";
      hash = "sha256-9HlCTsoJJITcQNh64oxE9MxAI0pgBF1hMeSTgA2BSjA=";
    };

    codeModeHostSrc = fetchurl {
      url = "https://github.com/openai/codex/releases/download/rust-v${version}/codex-code-mode-host-${target}.tar.gz";
      hash = "sha256-+VgwqGlZCVdmS7/Ge8ywh3OAa2k2cLrxWQgXb4m0zTE=";
    };

    sourceRoot = ".";
    dontConfigure = true;
    dontBuild = true;
    dontFixup = true;

    installPhase = ''
      runHook preInstall

      install -Dm755 codex-${target} "$out/bin/codex"
      tar -xzf "$codeModeHostSrc"
      install -Dm755 codex-code-mode-host-${target} "$out/bin/codex-code-mode-host"

      runHook postInstall
    '';

    meta = {
      description = "OpenAI Codex CLI";
      homepage = "https://developers.openai.com/codex/cli";
      license = lib.licenses.asl20;
      mainProgram = "codex";
      platforms = ["x86_64-linux"];
    };
  }
