#!/usr/bin/env bash

# remove-user.sh - Safe user removal script for unibe_clan
# This script removes users from user-list.nix and provides backup options

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAN_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
USER_LIST_FILE="$CLAN_ROOT/user-list.nix"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

usage() {
    echo "Usage: $0 [OPTIONS] USERNAME"
    echo
    echo "Safely remove a user from the clan configuration."
    echo
    echo "Options:"
    echo "  -h, --help              Show this help message"
    echo "  -n, --dry-run          Show what would be done without making changes"
    echo "  -b, --backup FILE      Create backup of user-list.nix before changes"
    echo "  -f, --force            Skip confirmation prompts"
    echo "  --keep-snapshots       Don't delete ZFS snapshots after user removal"
    echo "  --list-users           List current users and exit"
    echo
    echo "Examples:"
    echo "  $0 alice                    # Remove user alice (with confirmation)"
    echo "  $0 -f -b backup.nix bob     # Remove bob, force, backup to backup.nix"
    echo "  $0 -n charlie               # Dry run - show what would happen"
    echo "  $0 --list-users             # Show current users"
    echo
    echo "IMPORTANT: User data will be backed up as ZFS snapshots before deletion."
    echo "           Check /shared/deleted-users/ for additional file backups."
}

error() {
    echo -e "${RED}ERROR:${NC} $1" >&2
    exit 1
}

warning() {
    echo -e "${YELLOW}WARNING:${NC} $1" >&2
}

info() {
    echo -e "${BLUE}INFO:${NC} $1"
}

success() {
    echo -e "${GREEN}SUCCESS:${NC} $1"
}

# Parse command line arguments
DRY_RUN=false
FORCE=false
BACKUP_FILE=""
KEEP_SNAPSHOTS=false
LIST_USERS=false
USERNAME=""

while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            usage
            exit 0
            ;;
        -n|--dry-run)
            DRY_RUN=true
            shift
            ;;
        -f|--force)
            FORCE=true
            shift
            ;;
        -b|--backup)
            BACKUP_FILE="$2"
            shift 2
            ;;
        --keep-snapshots)
            KEEP_SNAPSHOTS=true
            shift
            ;;
        --list-users)
            LIST_USERS=true
            shift
            ;;
        -*)
            error "Unknown option: $1"
            ;;
        *)
            if [[ -n "$USERNAME" ]]; then
                error "Multiple usernames provided. Only one username allowed."
            fi
            USERNAME="$1"
            shift
            ;;
    esac
done

# Change to clan directory
cd "$CLAN_ROOT"

# Validate environment
if [[ ! -f "$USER_LIST_FILE" ]]; then
    error "user-list.nix not found at $USER_LIST_FILE"
fi

# Check if we can parse the current file
if ! nix-instantiate --parse "$USER_LIST_FILE" >/dev/null 2>&1; then
    error "user-list.nix has syntax errors. Please fix before proceeding."
fi

# List users and exit if requested
if [[ "$LIST_USERS" == "true" ]]; then
    info "Current users in $USER_LIST_FILE:"
    # Extract usernames from user-list.nix
    grep -E '^\s*name\s*=\s*"[^"]+";' "$USER_LIST_FILE" | sed -E 's/.*name\s*=\s*"([^"]+)".*/\1/' | sort
    exit 0
fi

# Validate username is provided
if [[ -z "$USERNAME" ]]; then
    error "Username is required. Use --list-users to see available users."
fi

# Validate username format
if [[ ! "$USERNAME" =~ ^[a-zA-Z][a-zA-Z0-9_-]*$ ]]; then
    error "Invalid username format: $USERNAME"
fi

# Check if user exists in configuration
if ! grep -q "name = \"$USERNAME\";" "$USER_LIST_FILE"; then
    error "User '$USERNAME' not found in configuration"
fi

info "Found user '$USERNAME' in configuration"

# Create backup if requested
if [[ -n "$BACKUP_FILE" ]]; then
    if [[ "$DRY_RUN" == "false" ]]; then
        cp "$USER_LIST_FILE" "$BACKUP_FILE"
        success "Backup created: $BACKUP_FILE"
    else
        info "Would create backup: $BACKUP_FILE"
    fi
fi

# Show what will be done
info "This will:"
echo "  1. Remove user '$USERNAME' from user-list.nix"
echo "  2. When deployed, create ZFS backup snapshots of user data"
echo "  3. Copy user data to /shared/deleted-users/ as additional backup"
echo "  4. Remove the user's ZFS dataset and system account"
echo "  5. Remove user's directories from /shared/"

# Confirmation unless forced
if [[ "$FORCE" == "false" && "$DRY_RUN" == "false" ]]; then
    echo
    echo -e "${YELLOW}Are you sure you want to remove user '$USERNAME'? (y/N)${NC}"
    read -r response
    if [[ ! "$response" =~ ^[Yy]$ ]]; then
        info "Aborted by user"
        exit 0
    fi
fi

# Find the user block in the file
USER_START_LINE=$(grep -n "name = \"$USERNAME\";" "$USER_LIST_FILE" | cut -d: -f1)

if [[ -z "$USER_START_LINE" ]]; then
    error "Could not locate user block for '$USERNAME'"
fi

# Find the opening brace before the name line
BLOCK_START_LINE=$USER_START_LINE
while [[ $BLOCK_START_LINE -gt 1 ]]; do
    if grep -q "^\s*{" <(sed -n "${BLOCK_START_LINE}p" "$USER_LIST_FILE"); then
        break
    fi
    ((BLOCK_START_LINE--))
done

# Find the closing brace after the name line
BLOCK_END_LINE=$USER_START_LINE
TOTAL_LINES=$(wc -l < "$USER_LIST_FILE")
BRACE_COUNT=0
while [[ $BLOCK_END_LINE -le $TOTAL_LINES ]]; do
    line=$(sed -n "${BLOCK_END_LINE}p" "$USER_LIST_FILE")
    # Count opening braces
    BRACE_COUNT=$((BRACE_COUNT + $(echo "$line" | grep -o '{' | wc -l)))
    # Count closing braces
    BRACE_COUNT=$((BRACE_COUNT - $(echo "$line" | grep -o '}' | wc -l)))

    # If we've closed all braces, we found the end
    if [[ $BRACE_COUNT -eq 0 && $BLOCK_END_LINE -gt $USER_START_LINE ]]; then
        break
    fi
    ((BLOCK_END_LINE++))
done

if [[ "$DRY_RUN" == "true" ]]; then
    info "DRY RUN - Would remove lines $BLOCK_START_LINE to $BLOCK_END_LINE from user-list.nix:"
    sed -n "${BLOCK_START_LINE},${BLOCK_END_LINE}p" "$USER_LIST_FILE" | sed 's/^/  | /'

    # Show what the result would look like
    info "Resulting configuration would be valid:"
    TEMP_FILE=$(mktemp)
    sed "${BLOCK_START_LINE},${BLOCK_END_LINE}d" "$USER_LIST_FILE" > "$TEMP_FILE"
    if nix-instantiate --parse "$TEMP_FILE" >/dev/null 2>&1; then
        success "Configuration would be valid after removal"
    else
        error "Configuration would be invalid after removal. Please check manually."
    fi
    rm "$TEMP_FILE"

    info "To apply changes, run without --dry-run"
    exit 0
fi

# Create temporary file with user removed
TEMP_FILE=$(mktemp)
sed "${BLOCK_START_LINE},${BLOCK_END_LINE}d" "$USER_LIST_FILE" > "$TEMP_FILE"

# Validate the result
if ! nix-instantiate --parse "$TEMP_FILE" >/dev/null 2>&1; then
    rm "$TEMP_FILE"
    error "Resulting configuration would be invalid. Aborting."
fi

# Apply the changes
mv "$TEMP_FILE" "$USER_LIST_FILE"
success "User '$USERNAME' removed from configuration"

# Instructions for deployment
echo
info "Next steps:"
echo "  1. Review the changes: git diff user-list.nix"
echo "  2. Deploy to machines: clan machines update <machine-name>"
echo "  3. Check backup snapshots: zfs list -t snapshot | grep deleted"
echo "  4. Check file backups: ls -la /shared/deleted-users/"

if [[ "$KEEP_SNAPSHOTS" == "true" ]]; then
    warning "ZFS snapshots will be kept indefinitely (--keep-snapshots used)"
else
    warning "ZFS snapshots will be automatically cleaned up after 30 days"
fi

success "User removal completed successfully"
