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
      settings = {
        name = "Your Name"; # TODO: Change this
        email = "your.email@example.com"; # TODO: Change this
      };
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
    # Option 3: Auto-switch to fish (enabled above in bash.initExtra)
    #   - Keeps bash as login shell but automatically switches to fish
    #   - Best of both worlds: compatibility + modern shell experience
    # Bash shell (traditional, widely compatible)
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
        # Auto-switch to fish if it's available and we're in an interactive session
        if [ -n "$PS1" ] && command -v fish > /dev/null 2>&1; then
          # Only switch if we're not already in fish (prevent infinite loop)
          if [ -z "$FISH_VERSION" ]; then
            exec fish
          fi
        fi
      '';
    };

    direnv = {
      enable = true;
      nix-direnv.enable = true;
    };
  };
}
