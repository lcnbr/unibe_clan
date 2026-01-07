{
  config,
  pkgs,
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

  # Basic packages - customize as needed
  home.packages = with pkgs; [
    helix
    htop
    tree
    curl
    bat
    fd
    ripgrep
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

    helix.enable = true;

    # Shell configuration - choose your preferred shell
    # Fish shell (modern, user-friendly)
    fish = {
      enable = true;
      interactiveShellInit = ''
        # Add your fish shell customizations here
        # Example: set -g fish_greeting ""  # Remove greeting
      '';
    };

    # Bash shell (traditional, widely compatible)

    # Shell Configuration Options:
    #
    # Option 1: Disable auto-switch (comment out bash.initExtra above)
    #   - Keep bash as login shell, manually type 'fish' when needed
    #   - Good if you prefer explicit shell switching
    #
    # Option 2: Change login shell permanently
    #   - Run: chsh -s $(which fish)
    #   - Log out and back in for the change to take effect
    #   - Run: chsh -s $(which bash) to switch back
    #
    # Option 3: Auto-switch to fish (DEFAULT - enabled above)
    #   - Keeps bash as login shell but automatically switches to fish
    #   - Best of both worlds: compatibility + modern shell experience
    bash = {
      enable = true;
      enableCompletion = true;
      # Uncomment to set bash aliases:
      # shellAliases = {
      #   ll = "ls -la";
      #   grep = "rg";
      # };

      # Automatically switch to fish when bash starts (Option 3 - enabled by default):
      initExtra = ''
        if [ -n "$PS1" ] && [ "$SHELL" != "$(which fish)" ] && command -v fish > /dev/null; then
          exec fish
        fi
      '';
    };

    direnv = {
      enable = true;
      nix-direnv.enable = true;
    };

    # Starship prompt configuration
    starship = {
      enable = true;
      settings = {
        # Add newline at start of prompt
        format = "$all$nix_shell$custom$character";

        # Right prompt showing VCS status
        right_format = "$git_branch$git_status";

        # Nix shell indicator - shows traditional nix-shell environments
        # For nix develop, see custom.nix_develop below
        nix_shell = {
          format = "[$symbol$state]($style) ";
          symbol = "❄️ ";
          style = "bold blue";
          impure_msg = ""; # Hide for nix develop, show with custom module
          pure_msg = "pure";
          unknown_msg = "nix";
          heuristic = false;
        };

        # Custom command to detect nix develop and show flake info
        # This distinguishes between:
        # - nix-shell: shows ❄️ (handled by nix_shell above)
        # - nix develop: shows 🚀 with flake name (handled here)
        # Environment variables checked:
        # - NIX_BUILD_TOP: present in nix develop
        # - IN_NIX_SHELL=impure: typical for nix develop
        # - name, FLAKE_INFO_NAME, PWD: sources for flake name
        custom.nix_develop = {
          description = "Nix develop environment with flake info";
          command = ''
            if [ -n "$IN_NIX_SHELL" ]; then
              # Check if we're in nix develop (has NIX_BUILD_TOP) vs nix-shell
              if [ -n "$NIX_BUILD_TOP" ] || [ "$IN_NIX_SHELL" = "impure" ]; then
                # This is likely nix develop
                FLAKE_NAME=""
                # Try multiple ways to get flake name
                if [ -n "$FLAKE_INFO_NAME" ]; then
                  FLAKE_NAME="$FLAKE_INFO_NAME"
                elif [ -n "$name" ] && [ "$name" != "nix-shell" ]; then
                  FLAKE_NAME="$name"
                elif [ -f "flake.nix" ]; then
                  FLAKE_NAME="$(basename "$PWD")"
                elif [ -n "$PRJ_ROOT" ]; then
                  FLAKE_NAME="$(basename "$PRJ_ROOT")"
                fi

                if [ -n "$FLAKE_NAME" ]; then
                  echo "🚀 $FLAKE_NAME"
                else
                  echo "🚀 dev"
                fi
              fi
            fi
          '';
          when = ''[ -n "$IN_NIX_SHELL" ] && ([ -n "$NIX_BUILD_TOP" ] || [ "$IN_NIX_SHELL" = "impure" ])'';
          style = "bold purple";
          format = "[$output]($style) ";
        };

        # Git branch configuration
        git_branch = {
          format = "[$symbol$branch]($style)";
          symbol = " ";
          style = "bold purple";
        };

        # Git status configuration
        git_status = {
          format = "([$all_status$ahead_behind]($style))";
          style = "bold red";
          conflicted = "⚡";
          ahead = "⇡";
          behind = "⇣";
          diverged = "⇕";
          untracked = "?";
          stashed = "$";
          modified = "!";
          staged = "+";
          renamed = "»";
          deleted = "✘";
        };

        # Character configuration (prompt symbol)
        character = {
          success_symbol = "[➜](bold green)";
          error_symbol = "[➜](bold red)";
          vicmd_symbol = "[V](bold yellow)";
        };

        # Directory configuration
        directory = {
          format = "[$path]($style)[$read_only]($read_only_style) ";
          style = "bold cyan";
          read_only = "🔒";
          truncation_length = 3;
          truncate_to_repo = true;
        };

        # Show hostname when connected via SSH
        hostname = {
          ssh_only = true;
          format = "[$hostname](bold yellow) ";
        };

        # Username configuration
        username = {
          show_always = false;
          format = "[$user]($style) ";
          style_user = "bold dimmed blue";
        };
      };
    };
  };
}
