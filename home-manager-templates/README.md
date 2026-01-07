# Home Manager User Environment

Welcome to your personal user environment management system! This setup automatically provides you with Home Manager configurations so you can customize your environment without affecting the system or other users.

## How It Works

When you first log in, the system automatically:
1. **Creates your Home Manager configuration** in `~/.config/home-manager/`
2. **Shows you a greeting** with next steps (only when config exists but isn't activated)
3. **Provides both Fish and Bash shells** with custom greeting integration

The greeting message appears in your shell until you activate Home Manager - then it disappears automatically.

## Getting Started

### Step 1: Activate Your Configuration

Your configuration is already created! Just activate it:

```bash
nh home switch .config/home-manager -b backup
```

### Step 2: Customize Your Environment

Edit your personal configuration:

```bash
cd ~/.config/home-manager
$EDITOR home.nix
```

### Step 3: Apply Changes

Whenever you make changes:

```bash
nh home switch
```

That's it! Changes apply immediately without system rebuilds or sudo.

## What's Already Set Up

Your `~/.config/home-manager/` directory contains:
- **`home.nix`** - Your personal configuration (generated from template)
- **`flake.nix`** - Nix flake definition for your setup
- **`.gitignore`** - Ignores build results

## Shell Integration

- **Fish Shell**: Default shell with custom `fish_greeting` function
- **Bash**: Also available with greeting integration
- **Automatic Greeting**: Shows setup instructions until Home Manager is activated
- **Debug Tool**: Use `check-hm-greeting` to troubleshoot greeting behavior

## What You Can Customize

### Install Personal Packages
```nix
home.packages = with pkgs; [
  python3
  nodejs
  ripgrep
  fd
];
```

### Configure Programs
```nix
programs = {
  git = {
    enable = true;
    userName = "Your Name";
    userEmail = "your.email@example.com";
  };

  fish = {
    enable = true;
    shellAliases = {
      ll = "ls -la";
      ".." = "cd ..";
      grep = "rg";
    };
    functions = {
      mkcd = "mkdir -p $argv[1]; and cd $argv[1]";
    };
  };

  helix = {
    enable = true;
    defaultEditor = true;
  };
  
  direnv = {
    enable = true;
    nix-direnv.enable = true;
  };
};
```

### Manage Dotfiles
```nix
home.file = {
  ".config/starship.toml".text = ''
    format = "$username$hostname$directory$git_branch$character"
    [character]
    success_symbol = "[➜](bold green)"
  '';
};
```

### Set Environment Variables
```nix
home.sessionVariables = {
  EDITOR = "helix";
  BROWSER = "firefox";
  PAGER = "less";
};
```


## Useful Commands

```bash
# Apply your configuration
nh home switch

# Search for packages
nh search package-name

# Update your packages
cd ~/.config/home-manager && nix flake update

# Check configuration for errors
cd ~/.config/home-manager && nix flake check

# See your Home Manager generations (for rollback)
home-manager generations
```

## Troubleshooting

### Configuration Errors
```bash
# Check for syntax errors
cd ~/.config/home-manager && nix flake check

# See detailed error messages
cd ~/.config/home-manager && nh home switch --show-trace
```

## Version Control Your Config

It's recommended to version control your personal configuration:

```bash
cd ~/.config/home-manager
git init
git add .
git commit -m "Initial Home Manager configuration"
```

## Finding More Options

- **Home Manager Manual**: https://nix-community.github.io/home-manager/
- **All Available Options**: https://home-manager-options.extranix.com/
- **Package Search**: https://search.nixos.org/packages
- **NixOS Options**: https://search.nixos.org/options
