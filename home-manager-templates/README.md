# Simple Home Manager Setup

This guide shows you how to manage your own user environment with Home Manager.

## Quick Start

1. **Initialize your configuration:**
   ```bash
   setup-home-manager
   ```

2. **Edit your settings:**
   ```bash
   cd ~/.config/home-manager
   $EDITOR home.nix
   ```

3. **Apply changes:**
   ```bash
   nh home switch
   ```

That's it! Your changes apply immediately without system rebuilds.

## What You Can Do

- **Install packages**: Add to `home.packages = with pkgs; [ ... ];`
- **Configure programs**: Use `programs.git.enable = true;` etc.
- **Manage dotfiles**: Use `home.file` to manage config files
- **Set environment**: Use `home.sessionVariables`

## Basic Configuration Example

```nix
{ config, pkgs, ... }: {
  home.username = builtins.getEnv "USER";
  home.homeDirectory = builtins.getEnv "HOME";
  home.stateVersion = "25.05";
  programs.home-manager.enable = true;

  # Install packages
  home.packages = with pkgs; [
    firefox
    python3
    nodejs
  ];

  # Configure programs
  programs = {
    git = {
      enable = true;
      userName = "Your Name";
      userEmail = "your.email@example.com";
    };

    helix.enable = true;
    
    direnv = {
      enable = true;
      nix-direnv.enable = true;
    };
  };

  # Environment variables
  home.sessionVariables = {
    EDITOR = "helix";
  };
}
```

## Common Commands

```bash
# Apply configuration
nh home switch

# Check for errors
cd ~/.config/home-manager && nix flake check

# Update packages
cd ~/.config/home-manager && nix flake update

# List generations (for rollback)
home-manager generations
```

## Finding Packages

Search for available packages:
```bash
nh search package-name
```

## Getting Help

- Home Manager manual: https://nix-community.github.io/home-manager/
- Available options: https://home-manager-options.extranix.com/
- Search packages: https://search.nixos.org/packages

## Tips

- Start simple and add complexity gradually
- Use version control: `git init` in your config directory
- Test changes frequently with `nh home switch`
- Check the advanced template for more examples

Your Home Manager configuration is completely independent from the system - you can update it anytime without affecting other users!