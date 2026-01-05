{
  pkgs,
  lib,
  ...
}: {
  home.packages = with pkgs; [
    viddy
    devenv
  ];
  programs = {
    gh = {
      enable = true;
      gitCredentialHelper = {
        enable = true;
      };
    };

    starship = {
      enable = true;
      settings = {
        add_newline = false;
        format = lib.concatStrings [
          "$shlvl"
          "$shell"
          "$username"
          "$hostname"
          "$nix_shell"
          "$git_branch"
          "$git_commit"
          "$git_state"
          "$git_status"
          "$directory"
          "$jobs"
          "$cmd_duration"
          "$character"
        ];
        scan_timeout = 10;
        # character = {
        #   success_symbol = "➜";
        #   error_symbol = "➜";
        # };
      };

      enableFishIntegration = true;
      enableNushellIntegration = true;
    };

    helix = {
      enable = true;
    };

    bash = {
      enable = true;
    };

    fish = {
      enable = true;
    };

    direnv = {
      enable = true;
      enableBashIntegration = true;
      nix-direnv.enable = true;
    };

    jujutsu = {
      enable = true;
      settings = {
        revset-aliases = {
          "immutable_heads()" = "builtin_immutable_heads() | remote_bookmarks()";
        };
      };
    };

    git = {
      enable = true;
      signing = {
        format = "ssh";
      };
    };

    zellij = {
      enable = true;
      settings = {
        theme = "catppuccin-frappe";
      };
    };
  };

  home.stateVersion = "25.05";
}
