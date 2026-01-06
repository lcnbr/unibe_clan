#!/usr/bin/env bash
# Interactive script to add a new user to the unibe clan configuration

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAN_ROOT="$(dirname "$SCRIPT_DIR")"
USER_LIST_FILE="$CLAN_ROOT/user-list.nix"

# Default admin SSH key - always included for all users (uses adminSshKey from user-list.nix)
DEFAULT_ADMIN_KEY="adminSshKey"

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

prompt() {
    echo -e "${BLUE}[PROMPT]${NC} $1"
}

echo "🚀 Interactive User Addition for Unibe Clan"
echo "==========================================="
echo ""

# Get username
while true; do
    prompt "Enter username (lowercase, letters/numbers/_/- only):"
    read -r USERNAME

    if [[ -z "$USERNAME" ]]; then
        warn "Username cannot be empty"
        continue
    fi

    if [[ ! "$USERNAME" =~ ^[a-z][a-z0-9_-]*$ ]]; then
        warn "Invalid username. Must start with lowercase letter and contain only lowercase letters, numbers, hyphens, and underscores."
        continue
    fi

    if grep -q "name = \"$USERNAME\"" "$USER_LIST_FILE"; then
        warn "User '$USERNAME' already exists"
        continue
    fi

    break
done

# Auto-assign UID
HIGHEST_UID=$(grep -o 'uid = [0-9]*' "$USER_LIST_FILE" | grep -o '[0-9]*' | sort -n | tail -1)
AUTO_UID=$((HIGHEST_UID + 1))

prompt "Enter UID (press Enter for auto-assigned: $AUTO_UID):"
read -r UID_INPUT
if [[ -z "$UID_INPUT" ]]; then
    USER_UID="$AUTO_UID"
else
    USER_UID="$UID_INPUT"
fi

# Validate UID
if [[ ! "$USER_UID" =~ ^[0-9]+$ ]] || [[ "$USER_UID" -lt 1000 ]]; then
    echo "❌ Invalid UID. Must be a number >= 1000"
    exit 1
fi

if grep -q "uid = $USER_UID" "$USER_LIST_FILE"; then
    echo "❌ UID $USER_UID is already in use"
    exit 1
fi

# Get SSH keys
echo ""
prompt "Enter SSH public keys (one per line, empty line to finish):"
SSH_KEYS=()
while true; do
    read -r SSH_KEY
    if [[ -z "$SSH_KEY" ]]; then
        break
    fi
    SSH_KEYS+=("$SSH_KEY")
    log "Added SSH key: ${SSH_KEY:0:50}..."
done

if [[ ${#SSH_KEYS[@]} -eq 0 ]]; then
    log "No additional user SSH keys provided. Will use default admin key."
fi

# Get additional groups
prompt "Enter additional groups (comma-separated, or press Enter for default 'nfs'):"
read -r GROUPS_INPUT
if [[ -z "$GROUPS_INPUT" ]]; then
    USER_GROUPS="nfs"
else
    USER_GROUPS="$GROUPS_INPUT"
fi

# Generate configuration
echo ""
log "Generating user configuration..."

# Convert SSH keys to nix list format
IFS=',' read -ra GROUP_ARRAY <<< "$USER_GROUPS"
GROUPS_NIX=""
for group in "${GROUP_ARRAY[@]}"; do
    group=$(echo "$group" | xargs) # trim whitespace
    if [[ -n "$GROUPS_NIX" ]]; then
        GROUPS_NIX="$GROUPS_NIX, \"$group\""
    else
        GROUPS_NIX="\"$group\""
    fi
done

# Convert SSH keys to nix list format - always include admin key
SSH_KEYS_NIX="        $DEFAULT_ADMIN_KEY"

if [[ ${#SSH_KEYS[@]} -gt 0 ]]; then
    for key in "${SSH_KEYS[@]}"; do
        SSH_KEYS_NIX="$SSH_KEYS_NIX\n        \"$key\""
    done
    log "Added ${#SSH_KEYS[@]} user SSH key(s) plus default admin key"
else
    log "Added default admin key (no additional user keys provided)"
fi

# Generate the user configuration
USER_CONFIG="    {
      name = \"$USERNAME\";
      isNormalUser = true;
      uid = $USER_UID;
      extraGroups = [$GROUPS_NIX];
      sshKeys = ["

if [[ ${#SSH_KEYS[@]} -gt 0 ]]; then
    USER_CONFIG="$USER_CONFIG
$SSH_KEYS_NIX"
fi

USER_CONFIG="$USER_CONFIG
      ];
    }"

# Show preview
echo ""
echo "📋 User Configuration Preview:"
echo "=============================="
echo -e "${BLUE}$USER_CONFIG${NC}"
echo ""

# Confirm addition
while true; do
    prompt "Add this user to the configuration? [y/N]:"
    read -r CONFIRM
    case $CONFIRM in
        [Yy]* ) break;;
        [Nn]* ) log "Cancelled by user"; exit 0;;
        "" ) log "Cancelled by user"; exit 0;;
        * ) warn "Please answer yes or no.";;
    esac
done

# Backup and modify
cp "$USER_LIST_FILE" "$USER_LIST_FILE.bak"
log "Created backup: $USER_LIST_FILE.bak"

# Add the user to the file
# Find the last user entry and add before the closing ]; of the users array
# Use a temporary file approach to handle multi-line insertions properly
awk -v user_config="$USER_CONFIG" '
    /^  ];$/ {
        print user_config
        print $0
        next
    }
    { print }
' "$USER_LIST_FILE" > "$USER_LIST_FILE.tmp"

if [[ $? -ne 0 ]]; then
    echo "❌ Failed to generate new user list"
    # Restore backup
    mv "$USER_LIST_FILE.bak" "$USER_LIST_FILE"
    rm -f "$USER_LIST_FILE.tmp"
    exit 1
fi

if ! mv "$USER_LIST_FILE.tmp" "$USER_LIST_FILE"; then
    echo "❌ Failed to replace $USER_LIST_FILE"
    # Restore backup
    mv "$USER_LIST_FILE.bak" "$USER_LIST_FILE"
    rm -f "$USER_LIST_FILE.tmp"
    exit 1
fi

echo ""
echo "✅ Successfully added user '$USERNAME'!"
echo ""
log "Next steps:"
echo "  1. Review changes: git diff $USER_LIST_FILE"
echo "  2. Commit changes: jj describe -m 'Add user $USERNAME'"
echo "  3. Deploy: clan machines update <machine-name>"
echo ""
log "The user will get Home Manager configuration automatically on next system activation."
echo "🐟 They'll have fish shell auto-switching enabled by default!"
