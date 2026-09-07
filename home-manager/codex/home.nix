{
  config,
  lib,
  pkgs,
  codexUsername,
  ...
}: let
  codexPackage = pkgs.callPackage ./package.nix {};
  sharedHomeSource = pkgs.writeText "codex-home.nix" (builtins.readFile ./home.nix);
  sharedPackageSource = pkgs.writeText "codex-package.nix" (builtins.readFile ./package.nix);
  standaloneFlakeSource = pkgs.writeText "codex-flake-${codexUsername}.nix" ''
    {
      description = "Unified Codex Home Manager configuration";

      inputs = {
        nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
        home-manager.url = "github:nix-community/home-manager";
        home-manager.inputs.nixpkgs.follows = "nixpkgs";
      };

      outputs = { nixpkgs, home-manager, ... }: {
        homeConfigurations."${codexUsername}" = home-manager.lib.homeManagerConfiguration {
          pkgs = nixpkgs.legacyPackages.x86_64-linux;
          modules = [ ./home.nix ];
          extraSpecialArgs.codexUsername = "${codexUsername}";
        };
      };
    }
  '';
  managedHomeManagerFiles = [
    {
      source = sharedHomeSource;
      target = "home.nix";
    }
    {
      source = sharedPackageSource;
      target = "package.nix";
    }
    {
      source = standaloneFlakeSource;
      target = "flake.nix";
    }
    {
      source = ./flake.lock;
      target = "flake.lock";
    }
  ];
  allowedUsernames = [
    "codex"
    "codex-1"
    "codex-2"
    "codex-3"
    "codex-dummy-0"
    "codex-dummy-1"
    "codex-dummy-2"
    "codex-dummy-3"
  ];
in {
  assertions = [
    {
      assertion = builtins.elem codexUsername allowedUsernames;
      message = "the shared Codex home module may only be used by its eight fixed itphlies users";
    }
  ];

  home.username = codexUsername;
  home.homeDirectory = "/home/${codexUsername}";
  home.stateVersion = "25.05";

  programs.home-manager.enable = true;

  home.packages =
    (with pkgs; [
      uv
      zellij
      vim
      devenv
      helix
      htop
      tree
      curl
      bat
      fd
      ripgrep
      nixfmt-rfc-style
      nil
      nixd
    ])
    ++ [codexPackage];

  # Home Manager normally links managed files back into the Nix store. A flake
  # cannot follow those out-of-tree links during pure evaluation, so install
  # regular files atomically instead. The identity-neutral home.nix and its
  # package expression are byte-identical for every account; only flake.nix
  # carries the username required by standalone `nh home switch`.
  home.activation.installUnifiedCodexHome = lib.hm.dag.entryAfter ["linkGeneration"] ''
    targetDir=${lib.escapeShellArg "${config.xdg.configHome}/home-manager"}
    backupSuffix=.before-codex-unification

    installUnifiedFile() {
      local sourcePath="$1"
      local targetPath="$2"
      local backupPath="$targetPath$backupSuffix"
      local stagedPath="$targetPath.unified-new"

      if [[ -v DRY_RUN ]]; then
        echo "Would install unified Codex config $targetPath"
        return
      fi

      mkdir -p -- "$targetDir"
      if { [[ -e "$targetPath" ]] || [[ -L "$targetPath" ]]; } \
        && ! cmp --quiet "$sourcePath" "$targetPath" \
        && [[ ! -e "$backupPath" ]] \
        && [[ ! -L "$backupPath" ]]; then
        mv -- "$targetPath" "$backupPath"
      fi

      install -m 0644 -T "$sourcePath" "$stagedPath"
      mv -fT -- "$stagedPath" "$targetPath"
    }

    ${lib.concatMapStringsSep "\n" (file: ''
        installUnifiedFile ${lib.escapeShellArg (toString file.source)} "$targetDir/${file.target}"
      '')
      managedHomeManagerFiles}
  '';

  programs = {
    git.enable = true;
    gh.enable = true;

    nh = {
      enable = true;
      homeFlake = "${config.xdg.configHome}/home-manager";
    };

    jujutsu.enable = true;
    nix-search-tv.enable = true;
    television.enable = true;
    helix.enable = true;

    fish = {
      enable = true;
      functions.fish_greeting.body = ''
        echo "🔬 ITP@unibe $(hostname) | Welcome $(whoami)!"
        echo ""
        echo "📝 Shared config: /common/nix/clan/home-manager/codex/home.nix"
        echo "🚀 Update: nh home switch"
        echo "🔍 Find packages: nh search <name>"
        echo ""
        echo "💬 Help: https://alphaloop.zulipchat.com/join/5azdr7ob7c3gdozln7vereyr/"
      '';
    };

    bash = {
      enable = true;
      enableCompletion = true;
    };

    starship = {
      enable = true;
      enableFishIntegration = true;
      enableBashIntegration = true;

      settings = {
        add_newline = false;
        format = lib.concatStrings [
          "$all"
          "$fill"
          "$direnv"
          "$shell"
          "$shlvl"
          "$time"
          "$line_break"
          "$directory"
          "$character"
        ];
        fill = {
          disabled = false;
          symbol = " ";
        };
        direnv.disabled = false;
        shell.disabled = false;
        shlvl = {
          disabled = false;
          format = "[$shlvl]($style) levels deep ";
        };
        time.disabled = false;
        username = {
          disabled = false;
          show_always = true;
        };
        hostname.disabled = false;
      };
    };

    direnv = {
      enable = true;
      nix-direnv.enable = true;
    };
  };
}
