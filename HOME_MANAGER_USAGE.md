# Home Manager Self-Management Guide

This guide shows you how to manage your own user environment using Home Manager, giving you full control without needing system administrator access.

## Quick Start

### 1. Initial Setup

Run the setup script on any machine:

```bash
setup-home-manager
```

This creates `~/.config/home-manager/` with initial configuration files.

### 2. Customize Your Environment

Edit your configuration:

```bash
cd ~/.config/home-manager
$EDITOR home.nix
```

### 3. Apply Changes

```bash
home-manager switch --flake ~/.config/home-manager#$(whoami)
```

That's it! Your changes are applied immediately.

## What You Can Manage

- **Packages**: Install any software from nixpkgs
- **Dotfiles**: Manage configuration files declaratively  
- **Programs**: Configure shells, editors, git, etc.
- **Services**: Run user services (gpg-agent, etc.)
- **Environment**: Set variables, aliases, functions

## Example Configurations

### Basic Setup

```nix
{ config, pkgs, ... }:

{
  home.username = builtins.getEnv "USER";
  home.homeDirectory = builtins.getEnv "HOME";
  home.stateVersion = "25.05";

  programs.home-manager.enable = true;

  # Install packages
  home.packages = with pkgs; [
    git
    helix
    ripgrep
    htop
  ];

  # Configure programs
  programs = {
    git = {
      enable = true;
      userName = "Your Name";
      userEmail = "your.email@example.com";
    };

    helix.enable = true;
    
    starship.enable = true;
  };
}
```

### Development Environment

```nix
{ config, pkgs, ... }:

{
  # ... basic setup ...

  home.packages = with pkgs; [
    # Development tools
    nodejs
    python3
    rustc
    cargo
    go
    
    # Utilities
    jq
    curl
    tree
    bat
    fd
  ];

  programs = {
    direnv = {
      enable = true;
      nix-direnv.enable = true;
    };

    fish = {
      enable = true;
      shellAliases = {
        ll = "ls -la";
        grep = "rg";
      };
    };
  };
}
```

## Version Control Your Configuration

Initialize git in your config directory:

```bash
cd ~/.config/home-manager
git init
git add .
git commit -m "Initial Home Manager configuration"

# Optional: push to remote repository
git remote add origin git@github.com:yourusername/home-manager-config.git
git push -u origin main
```

## Common Workflows

### Adding New Packages

1. Edit `home.nix`, add packages to `home.packages`
2. Run `home-manager switch --flake ~/.config/home-manager#$(whoami)`

### Configuring Applications

1. Add program configuration to `programs.{name}` section
2. Apply with `home-manager switch --flake ~/.config/home-manager#$(whoami)`

### Managing Dotfiles

```nix
# In your home.nix
home.file = {
  ".gitignore_global".text = ''
    .DS_Store
    *.swp
    result
  '';
  
  ".config/myapp/config.toml".source = ./dotfiles/myapp-config.toml;
};
```

### Rolling Back Changes

```bash
# List previous generations
home-manager generations

# Rollback to previous generation  
home-manager switch --rollback

# Switch to specific generation
/nix/store/xxx-home-manager-generation/activate
```

## Useful Commands

```bash
# Apply configuration
home-manager switch --flake ~/.config/home-manager#$(whoami)

# Check configuration syntax
cd ~/.config/home-manager && nix flake check

# Update flake inputs
cd ~/.config/home-manager && nix flake update

# List installed packages
home-manager packages

# Remove old generations (cleanup)
home-manager expire-generations "-30 days"
```

## Templates and Examples

Check the `home-manager-templates/` directory in the system repository for:

- Advanced configuration examples
- Program-specific setups
- Migration guides

## Getting Help

- **Home Manager Manual**: https://nix-community.github.io/home-manager/
- **Available Options**: https://home-manager-options.extranix.com/
- **NixOS Wiki**: https://nixos.wiki/wiki/Home_Manager
- **Community**: https://discourse.nixos.org/

## Troubleshooting

### Configuration Errors

```bash
cd ~/.config/home-manager
nix flake check
```

### Can't Find Packages

Search for packages:
```bash
nix search nixpkgs firefox
```

### Conflicts with System

If switching from system-managed configuration, clean up any conflicting files and restart your session.

### Permission Issues

Home Manager should only manage files in your home directory. System-wide changes still require administrator access.

---

**Remember**: Your Home Manager configuration is independent from the system configuration. You can update it anytime without affecting other users or requiring system rebuilds!