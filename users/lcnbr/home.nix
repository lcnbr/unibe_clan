{...}: {
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

    jujutsu = {
      enable = true;
      settings={
        user={
          email="im@lcnbr.ch";
          name="Lucien Huber";
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
