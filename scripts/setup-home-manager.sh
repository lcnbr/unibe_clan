#!/usr/bin/env bash
# Setup standalone Home Manager for a user
# This allows users to manage their own home configurations independently

set -euo pipefail

USER=${USER:-$(whoami)}
HOME_DIR=${HOME:-/home/$USER}

echo "Setting up standalone Home Manager for user: $USER"

# Check if Home Manager is available
if ! command -v home-manager &> /dev/null; then
    echo "Error: home-manager command not found in PATH"
    echo "Please ensure Home Manager is installed on the system"
    exit 1
fi

echo "Home Manager is available, proceeding with setup..."

# Create Home Manager configuration directory
HM_CONFIG_DIR="$HOME_DIR/.config/home-manager"
mkdir -p "$HM_CONFIG_DIR"

# Create initial home.nix if it doesn't exist
if [[ ! -f "$HM_CONFIG_DIR/home.nix" ]]; then
    # Try to find the default template
    TEMPLATE_PATH=""

    # Look for template in common locations
    for template_location in \
        "/etc/nixos/home-manager-templates/default-standalone-home.nix" \
        "/run/current-system/sw/share/home-manager-templates/default-standalone-home.nix" \
        "$HOME/.local/share/home-manager-templates/default-standalone-home.nix"
    do
        if [[ -f "$template_location" ]]; then
            TEMPLATE_PATH="$template_location"
            break
        fi
    done

    if [[ -n "$TEMPLATE_PATH" ]]; then
        # Copy template and substitute username and home directory
        sed -e "s/USERNAME_PLACEHOLDER/$USER/g" \
            "$TEMPLATE_PATH" > "$HM_CONFIG_DIR/home.nix"
        echo "Created initial configuration based on system template at $HM_CONFIG_DIR/home.nix"
    else
        # Fallback to minimal template
        cat > "$HM_CONFIG_DIR/home.nix" << 'EOF'
{ config, pkgs, ... }:

{
  home.username = "$USER";
  home.homeDirectory = "$HOME_DIR";
  home.stateVersion = "25.05";
  programs.home-manager.enable = true;

  # Basic packages - customize as needed
  home.packages = with pkgs; [
    git
    helix
    htop
    tree
    curl
  ];

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
EOF
        echo "Created basic initial configuration at $HM_CONFIG_DIR/home.nix"
    fi
    echo "Please edit this file to customize your home environment"
fi

# Create flake.nix for the user's Home Manager setup
if [[ ! -f "$HM_CONFIG_DIR/flake.nix" ]]; then
    cat > "$HM_CONFIG_DIR/flake.nix" << EOF
{
  description = "Home Manager configuration for $USER";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    home-manager.url = "github:nix-community/home-manager";
    home-manager.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = { nixpkgs, home-manager, ... }: {
    homeConfigurations."$USER" = home-manager.lib.homeManagerConfiguration {
      pkgs = nixpkgs.legacyPackages.x86_64-linux;
      modules = [ ./home.nix ];
    };
  };
}
EOF
    echo "Created flake configuration at $HM_CONFIG_DIR/flake.nix"
fi

# Create .gitignore
if [[ ! -f "$HM_CONFIG_DIR/.gitignore" ]]; then
    cat > "$HM_CONFIG_DIR/.gitignore" << 'EOF'
result
result-*
EOF
    echo "Created .gitignore at $HM_CONFIG_DIR/.gitignore"
fi

echo ""
echo "Setup complete! Next steps:"
echo ""
echo "1. Edit your configuration:"
echo "   \$EDITOR $HM_CONFIG_DIR/home.nix"
echo ""
echo "2. Apply your configuration:"
echo "   cd $HM_CONFIG_DIR"
echo "   home-manager switch --flake .#$USER"
echo ""
echo "3. For future updates:"
echo "   home-manager switch --flake $HM_CONFIG_DIR#$USER"
echo ""
echo "Templates and documentation available at /etc/home-manager-templates/"
