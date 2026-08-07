{
  config,
  pkgs,
  lib,
  ...
}: {
  # Home Manager needs a bit of information about you and the paths it should
  # manage.
  home.username = "USERNAME_PLACEHOLDER";
  home.homeDirectory = "/home/USERNAME_PLACEHOLDER";

  # This value determines the Home Manager release that your configuration is
  # compatible with. This helps avoid breakage when a new Home Manager release
  # introduces backwards incompatible changes.
  home.stateVersion = "25.05";

  # Let Home Manager install and manage itself.
  programs.home-manager.enable = true;

  home.packages = with pkgs; [
    uv
    zellij
    vim
    devenv
    codex
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
  ];

  # Basic program configurations
  programs = {
    git = {
      enable = true;
      settings = {
        user = {
          name = "CHANGE_ME";
          email = "CHANGE@ME.ch";
        };
      };
    };
    nh = {
      enable = true;
      homeFlake = "${config.xdg.configHome}/home-manager";
    };

    jujutsu = {
      enable = true;
    };

    nix-search-tv = {
      enable = true;
    };
    television.enable = true;

    helix.enable = true;

    fish = {
      enable = true;
      functions = {
        fish_greeting = {
          body = ''
            echo "🔬 ITP@unibe $(hostname) | Welcome $(whoami)!"
            echo ""
            echo "📝 Edit home config: \$EDITOR ~/.config/home-manager/home.nix"
            echo "🚀 Update: nh home switch"
            echo "🔍 Find packages: nh search <name>"
            echo ""
            echo "💬 Help: https://alphaloop.zulipchat.com/join/5azdr7ob7c3gdozln7vereyr/"
          '';
        };
      };
    };

    # Bash shell (traditional, widely compatible)
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
        direnv = {
          disabled = false;
        };
        shell = {
          disabled = false;
        };
        shlvl = {
          disabled = false;
          format = "[$shlvl]($style) levels deep ";
        };
        time = {
          disabled = false;
        };
        username = {
          disabled = false;
          show_always = true;
          # format = "[$user]($style)@";
        };

        hostname = {
          disabled = false;
          # format = "[$hostname]($style)";
        };
      };
    };

    direnv = {
      enable = true;
      nix-direnv.enable = true;
    };
  };
}
