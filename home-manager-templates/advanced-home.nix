{
  config,
  pkgs,
  lib,
  ...
}: {
  # Home Manager needs a bit of information about you and the paths it should
  # manage.
  home.username = builtins.getEnv "USER";
  home.homeDirectory = builtins.getEnv "HOME";

  # This value determines the Home Manager release that your configuration is
  # compatible with. This helps avoid breakage when a new Home Manager release
  # introduces backwards incompatible changes.
  home.stateVersion = "25.05";

  # Let Home Manager install and manage itself.
  programs.home-manager.enable = true;

  # Packages to install
  home.packages = with pkgs; [
    # System utilities
    htop
    btop
    tree
    ripgrep
    fd
    bat
    eza
    zoxide
    fzf
    jq
    curl
    wget
    unzip

    # Development tools
    git
    gh
    delta
    lazygit

    # Text editors
    neovim
    helix

    # Terminal multiplexers
    tmux
    zellij

    # Programming languages and tools
    nodejs
    python3
    rustc
    cargo
    go

    # Nix tools
    nix-tree
    nix-output-monitor
    devenv

    # Media and productivity
    imagemagick
    ffmpeg
    pandoc

    # Network tools
    nmap
    tcpdump
    wireshark-cli
  ];

  # Program configurations
  programs = {
    # Shell configuration
    bash = {
      enable = true;
      enableCompletion = true;
      historyControl = ["ignoredups" "ignorespace"];
      historySize = 10000;
      shellAliases = {
        ll = "eza -l";
        la = "eza -la";
        lt = "eza --tree";
        cat = "bat";
        grep = "rg";
        find = "fd";
      };
      bashrcExtra = ''
        # Custom bash configuration
        export EDITOR="helix"
        export BROWSER="firefox"

        # Better history
        shopt -s histappend
        PROMPT_COMMAND="history -a; history -c; history -r; $PROMPT_COMMAND"
      '';
    };

    fish = {
      enable = true;
      shellAliases = {
        ll = "eza -l";
        la = "eza -la";
        lt = "eza --tree";
        cat = "bat";
        grep = "rg";
        find = "fd";
      };
      functions = {
        mkcd = "mkdir -p $argv[1]; and cd $argv[1]";
        backup = "cp $argv[1] $argv[1].bak";
      };
      shellInit = ''
        # Custom fish initialization
        set -gx EDITOR helix
        set -gx BROWSER firefox
      '';
    };

    # Git configuration
    git = {
      enable = true;
      userName = "Your Name";
      userEmail = "your.email@example.com";

      aliases = {
        co = "checkout";
        br = "branch";
        ci = "commit";
        st = "status";
        unstage = "reset HEAD --";
        last = "log -1 HEAD";
        visual = "!gitk";
        graph = "log --graph --pretty=format:'%Cred%h%Creset -%C(yellow)%d%Creset %s %Cgreen(%cr) %C(bold blue)<%an>%Creset' --abbrev-commit";
      };

      extraConfig = {
        init.defaultBranch = "main";
        core.editor = "helix";
        pull.rebase = false;
        push.default = "simple";
        diff.tool = "delta";
        merge.tool = "delta";
      };

      delta = {
        enable = true;
        options = {
          navigate = true;
          light = false;
          side-by-side = true;
          line-numbers = true;
        };
      };
    };

    # GitHub CLI
    gh = {
      enable = true;
      gitCredentialHelper = {
        enable = true;
        hosts = ["github.com" "gist.github.com"];
      };
    };

    # Starship prompt
    starship = {
      enable = true;
      enableBashIntegration = true;
      enableFishIntegration = true;
      settings = {
        format = "$all$character";
        right_format = "$time";

        character = {
          success_symbol = "[➜](bold green)";
          error_symbol = "[➜](bold red)";
        };

        time = {
          disabled = false;
          format = "[$time]($style) ";
          style = "bright-blue";
        };

        git_branch = {
          format = "[$symbol$branch]($style) ";
          symbol = "🌱 ";
        };

        git_status = {
          format = "([\\[$all_status$ahead_behind\\]]($style) )";
          conflicted = "🏳";
          up_to_date = "✓";
          untracked = "?";
          ahead = "⇡\${count}";
          diverged = "⇕⇡\${ahead_count}⇣\${behind_count}";
          behind = "⇣\${count}";
          stashed = "$";
          modified = "!";
          staged = "+";
          renamed = "»";
          deleted = "✘";
        };

        nix_shell = {
          format = "via [$symbol$state( \\($name\\))]($style) ";
        };
      };
    };

    # Direnv for development environments
    direnv = {
      enable = true;
      enableBashIntegration = true;
      enableFishIntegration = true;
      nix-direnv.enable = true;
    };

    # Zoxide for smart directory jumping
    zoxide = {
      enable = true;
      enableBashIntegration = true;
      enableFishIntegration = true;
    };

    # FZF fuzzy finder
    fzf = {
      enable = true;
      enableBashIntegration = true;
      enableFishIntegration = true;
      defaultCommand = "fd --type f";
      defaultOptions = [
        "--height 40%"
        "--layout=reverse"
        "--border"
        "--preview 'bat --color=always --style=numbers --line-range=:500 {}'"
      ];
    };

    # Helix editor
    helix = {
      enable = true;
      settings = {
        theme = "catppuccin_frappe";
        editor = {
          line-number = "relative";
          mouse = true;
          cursor-shape = {
            insert = "bar";
            normal = "block";
            select = "underline";
          };
          file-picker = {
            hidden = false;
          };
          auto-save = true;
          rulers = [80 120];
          color-modes = true;
          cursorline = true;
          gutters = ["diagnostics" "spacer" "line-numbers" "spacer" "diff"];
          statusline = {
            left = ["mode" "spinner"];
            center = ["file-name" "file-modification-indicator"];
            right = ["diagnostics" "selections" "register" "position" "file-encoding"];
          };
          lsp = {
            display-messages = true;
            display-inlay-hints = true;
          };
          indent-guides = {
            render = true;
            character = "╎";
            skip-levels = 1;
          };
        };
        keys.normal = {
          space.space = "file_picker";
          space.w = ":w";
          space.q = ":q";
          esc = ["collapse_selection" "keep_primary_selection"];
        };
      };
      languages = {
        language-server.nil = {
          command = "nil";
        };
        language = [
          {
            name = "nix";
            language-servers = ["nil"];
            formatter = {command = "alejandra";};
            auto-format = true;
          }
        ];
      };
    };

    # Tmux terminal multiplexer
    tmux = {
      enable = true;
      clock24 = true;
      historyLimit = 100000;
      keyMode = "vi";
      mouse = true;
      prefix = "C-a";

      extraConfig = ''
        # Better colors
        set -g default-terminal "screen-256color"

        # Start windows and panes at 1
        set -g base-index 1
        setw -g pane-base-index 1

        # Renumber windows when one is closed
        set -g renumber-windows on

        # Split panes using | and -
        bind | split-window -h
        bind - split-window -v
        unbind '"'
        unbind %

        # Switch panes using Alt-arrow without prefix
        bind -n M-Left select-pane -L
        bind -n M-Right select-pane -R
        bind -n M-Up select-pane -U
        bind -n M-Down select-pane -D

        # Reload config
        bind r source-file ~/.config/tmux/tmux.conf

        # Status bar styling
        set -g status-bg black
        set -g status-fg white
        set -g status-interval 5
        set -g status-left-length 90
        set -g status-right-length 60
        set -g status-left "#[fg=Green]#(whoami)#[fg=white]::#[fg=blue]#(hostname -s)#[fg=white]::#[fg=yellow]#(curl ipecho.net/plain;echo) "
        set -g status-justify left
        set -g status-right '#[fg=Cyan]#S #[fg=white]%a %d %b %R'
      '';
    };

    # Zellij terminal multiplexer (alternative to tmux)
    zellij = {
      enable = true;
      settings = {
        theme = "catppuccin-frappe";
        default_shell = "fish";
        pane_frames = false;
        simplified_ui = true;
        copy_command = "wl-copy";
        copy_clipboard = "primary";
        copy_on_select = false;
        scrollback_editor = "helix";
      };
    };

    # Jujutsu VCS
    jujutsu = {
      enable = true;
      settings = {
        user = {
          email = "your.email@example.com";
          name = "Your Name";
        };
        ui = {
          default-command = "log";
          editor = "helix";
        };
        revset-aliases = {
          "immutable_heads()" = "builtin_immutable_heads() | remote_bookmarks()";
        };
      };
    };
  };

  # XDG configuration
  xdg = {
    enable = true;
    userDirs = {
      enable = true;
      createDirectories = true;
    };
  };

  # Environment variables
  home.sessionVariables = {
    EDITOR = "helix";
    BROWSER = "firefox";
    TERMINAL = "alacritty";
    PAGER = "less -R";
    MANPAGER = "less -R";

    # Nix-specific
    NIX_BUILD_CORES = "0";

    # Development
    CARGO_HOME = "$HOME/.cargo";
    GOPATH = "$HOME/go";

    # History
    HISTCONTROL = "ignoreboth";
    HISTSIZE = "100000";
    HISTFILESIZE = "100000";
  };

  # Custom dotfiles and configuration files
  home.file = {
    # Example: Custom gitignore
    ".config/git/ignore".text = ''
      # Nix
      result
      result-*

      # IDE
      .vscode/
      .idea/

      # OS
      .DS_Store
      Thumbs.db

      # Temporary files
      *.tmp
      *.swp
      *.swo
      *~
    '';

    # Example: Custom inputrc for better readline behavior
    ".inputrc".text = ''
      # Better tab completion
      set completion-ignore-case on
      set completion-map-case on
      set show-all-if-ambiguous on
      set show-all-if-unmodified on

      # Better history search
      "\e[A": history-search-backward
      "\e[B": history-search-forward
      "\e[C": forward-char
      "\e[D": backward-char

      # Alt-left/right for word navigation
      "\e\e[C": forward-word
      "\e\e[D": backward-word
      "\e[1;3C": forward-word
      "\e[1;3D": backward-word
    '';
  };

  # Services that should run in the user session
  services = {
    # Example: GPG agent
    gpg-agent = {
      enable = true;
      defaultCacheTtl = 1800;
      enableSshSupport = true;
    };
  };

  # Font configuration (if using a desktop environment)
  fonts.fontconfig.enable = true;

  # GTK theming (if using GTK applications)
  gtk = {
    enable = true;
    theme = {
      name = "Adwaita-dark";
      package = pkgs.gnome.gnome-themes-extra;
    };
  };

  # Custom systemd user services
  systemd.user.services = {
    # Example: Auto-sync dotfiles
    # dotfiles-sync = {
    #   Unit = {
    #     Description = "Sync dotfiles repository";
    #   };
    #   Service = {
    #     Type = "oneshot";
    #     ExecStart = "${pkgs.git}/bin/git -C %h/.config/home-manager pull";
    #   };
    # };
  };

  # Custom systemd user timers
  systemd.user.timers = {
    # Example: Auto-sync dotfiles every hour
    # dotfiles-sync = {
    #   Unit = {
    #     Description = "Sync dotfiles repository hourly";
    #   };
    #   Timer = {
    #     OnCalendar = "hourly";
    #     Persistent = true;
    #   };
    #   Install = {
    #     WantedBy = [ "timers.target" ];
    #   };
    # };
  };
}
