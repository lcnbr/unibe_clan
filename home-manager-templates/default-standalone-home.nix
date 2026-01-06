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
    git
    helix
    htop
    tree
    curl
  ];

  # Basic program configurations
  programs = {
    git = {
      enable = true;
      userName = "Your Name"; # TODO: Change this
      userEmail = "your.email@example.com"; # TODO: Change this
    };

    helix.enable = true;

    bash = {
      enable = true;
      enableCompletion = true;
    };

    direnv = {
      enable = true;
      nix-direnv.enable = true;
    };
  };
}
