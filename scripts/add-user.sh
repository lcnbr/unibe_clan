#!/usr/bin/env bash
# Script to add a new user to the unibe clan configuration

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAN_ROOT="$(dirname "$SCRIPT_DIR")"
USER_LIST_FILE="$CLAN_ROOT/user-list.nix"

# Default admin SSH key - always included for all users (uses adminSshKey from user-list.nix)
DEFAULT_ADMIN_KEY="adminSshKey"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

usage() {
    echo "Usage: $0 [OPTIONS] <username>"
    echo ""
    echo "Add a new user to the unibe clan configuration"
    echo ""
    echo "Options:"
    echo "  -h, --help              Show this help message"
    echo "  -u, --uid UID           Specify user ID (auto-assigned if not provided)"
    echo "  -s, --ssh-key KEY       SSH public key (can be used multiple times)"
    echo "  -g, --groups GROUPS     Additional groups (comma-separated, default: nfs)"
    echo "  -d, --dry-run           Show what would be added without modifying files"
    echo ""
    echo "Examples:"
    echo "  $0 newuser"
    echo "  $0 -u 1200 -s 'ssh-ed25519 AAAA...' -g 'nfs,wheel' newuser"
    echo "  $0 --dry-run newuser"
}

log() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1" >&2
}

# Parse command line arguments
USERNAME=""
USER_UID=""
SSH_KEYS=()
USER_GROUPS="nfs"
DRY_RUN=false

while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            usage
            exit 0
            ;;
        -u|--uid)
            USER_UID="$2"
            shift 2
            ;;
        -s|--ssh-key)
            SSH_KEYS+=("$2")
            shift 2
            ;;
        -g|--groups)
            USER_GROUPS="$2"
            shift 2
            ;;
        -d|--dry-run)
            DRY_RUN=true
            shift
            ;;
        -*)
            error "Unknown option $1"
            usage
            exit 1
            ;;
        *)
            if [[ -z "$USERNAME" ]]; then
                USERNAME="$1"
            else
                error "Username already specified: $USERNAME"
                usage
                exit 1
            fi
            shift
            ;;
    esac
done

if [[ -z "$USERNAME" ]]; then
    error "Username is required"
    usage
    exit 1
fi

# Validate username
if [[ ! "$USERNAME" =~ ^[a-z][a-z0-9_-]*$ ]]; then
    error "Invalid username. Must start with lowercase letter and contain only lowercase letters, numbers, hyphens, and underscores."
    exit 1
fi

# Check if user already exists
if grep -q "name = \"$USERNAME\"" "$USER_LIST_FILE"; then
    error "User '$USERNAME' already exists in $USER_LIST_FILE"
    exit 1
fi

# Auto-assign UID if not provided
if [[ -z "$USER_UID" ]]; then
    # Find the highest existing UID and add 1
    HIGHEST_UID=$(grep -o 'uid = [0-9]*' "$USER_LIST_FILE" | grep -o '[0-9]*' | sort -n | tail -1)
    USER_UID=$((HIGHEST_UID + 1))
    log "Auto-assigned UID: $USER_UID"
fi

# Validate UID
if [[ ! "$USER_UID" =~ ^[0-9]+$ ]] || [[ "$USER_UID" -lt 1000 ]]; then
    error "Invalid UID. Must be a number >= 1000"
    exit 1
fi

# Check if UID is already in use
if grep -q "uid = $USER_UID" "$USER_LIST_FILE"; then
    error "UID $USER_UID is already in use"
    exit 1
fi

# Convert groups to nix list format
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
      sshKeys = [
$SSH_KEYS_NIX
      ];
    }"

echo ""
log "New user configuration:"
echo -e "${BLUE}$USER_CONFIG${NC}"
echo ""

if [[ "$DRY_RUN" == true ]]; then
    log "Dry run mode - no changes made"
    exit 0
fi

# Prompt for confirmation
echo -n "Add this user to $USER_LIST_FILE? [y/N]: "
read -r CONFIRM
if [[ ! "$CONFIRM" =~ ^[Yy]$ ]]; then
    log "Cancelled by user"
    exit 0
fi

# Backup the original file
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
    error "Failed to generate new user list"
    # Restore backup
    mv "$USER_LIST_FILE.bak" "$USER_LIST_FILE"
    rm -f "$USER_LIST_FILE.tmp"
    exit 1
fi

if ! mv "$USER_LIST_FILE.tmp" "$USER_LIST_FILE"; then
    error "Failed to replace $USER_LIST_FILE"
    # Restore backup
    mv "$USER_LIST_FILE.bak" "$USER_LIST_FILE"
    rm -f "$USER_LIST_FILE.tmp"
    exit 1
fi

log "Successfully added user '$USERNAME' to $USER_LIST_FILE"

echo ""
log "Next steps:"
echo "  1. Review the changes: git diff $USER_LIST_FILE"
echo "  2. Commit the changes: jj describe -m 'Add user $USERNAME'"
echo "  3. Deploy to machines: clan machines update <machine-name>"
echo ""
log "The user will get a Home Manager configuration automatically on next system activation."
