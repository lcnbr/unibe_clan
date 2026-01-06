# User Management Scripts

This directory contains scripts to streamline adding new users to the unibe clan configuration.

## Scripts

### `add-user-interactive.sh` (Recommended for beginners)

Interactive script that guides you through adding a new user step-by-step.

```bash
./scripts/add-user-interactive.sh
```

Features:
- ✅ Interactive prompts for all user details
- ✅ Automatic UID assignment
- ✅ Input validation
- ✅ Configuration preview before applying
- ✅ Automatic backup creation

### `add-user.sh` (For advanced users)

Command-line script with options for scripted/automated user addition.

```bash
# Basic usage
./scripts/add-user.sh newuser

# Advanced usage
./scripts/add-user.sh -u 1200 -s 'ssh-ed25519 AAAA...' -g 'nfs,wheel' newuser

# Dry run (preview without changes)
./scripts/add-user.sh --dry-run newuser
```

Options:
- `-u, --uid UID`: Specify user ID (auto-assigned if not provided)
- `-s, --ssh-key KEY`: SSH public key (can be used multiple times)
- `-g, --groups GROUPS`: Additional groups (comma-separated, default: nfs)
- `-d, --dry-run`: Show what would be added without modifying files

## Workflow

1. **Add User:**
   ```bash
   ./scripts/add-user-interactive.sh
   ```

2. **Review Changes:**
   ```bash
   git diff user-list.nix
   ```

3. **Commit Changes:**
   ```bash
   jj describe -m "Add user <username>"
   ```

4. **Deploy to Machines:**
   ```bash
   clan machines update <machine-name>
   ```

## What Users Get Automatically

After deployment, new users automatically receive:

- 🏠 **Home Manager configuration** in `~/.config/home-manager/`
- 🐟 **Fish shell** with auto-switching from bash (best of both worlds)
- 📝 **Template configuration** with helpful examples and comments
- 🚀 **Greeting message** showing how to activate their environment

## User Experience

1. **First Login:** User sees greeting with activation instructions
2. **Activate Home Manager:** `home-manager switch --flake ~/.config/home-manager#$(whoami)`
3. **Modern Shell:** Fish shell with syntax highlighting, completions, etc.
4. **Customization:** Edit `~/.config/home-manager/home.nix` to customize environment

## Examples

### Add user with SSH key
```bash
./scripts/add-user.sh -s 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA...' alice
```

### Add user with specific UID and multiple groups
```bash
./scripts/add-user.sh -u 1500 -g 'nfs,wheel,docker' -s 'ssh-rsa AAAAB3N...' bob
```

### Preview changes without applying
```bash
./scripts/add-user.sh --dry-run charlie
```

## Notes

- UIDs are auto-assigned starting from the highest existing UID + 1
- All users get `nfs` group by default
- SSH keys are required for remote login
- Users manage their own Home Manager configurations after initial setup
- The system handles all the complex setup automatically