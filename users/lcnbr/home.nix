{pkgs, ...}: {
  home.packages = with pkgs; [
    viddy
  ];
  programs = {
    gh = {
      enable = true;
      gitCredentialHelper = {
        enable = true;
      };
    };

    starship = {
      enableNushellIntegration = true;
      enable = true;
    };

    helix = {
      enable = true;
    };

    direnv = {
      enable = true;
      enableBashIntegration = true;
      enableNushellIntegration = true;
      nix-direnv.enable = true;
    };

    jujutsu = {
      enable = true;
      settings = {
        user = {
          email = "im@lcnbr.ch";
          name = "Lucien Huber";
        };
        revset-aliases = {
          "immutable_heads()" = "builtin_immutable_heads() | remote_bookmarks()";
        };
      };
    };

    # git = {
    #   enable = true;
    #   userName = "lcnbr";
    #   userEmail = "im@lcnbr.ch";
    #   signing={
    #     format="ssh";
    #   };
    # };

    nushell = {
      enable = true;
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
