#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAN_ROOT="$(dirname "$SCRIPT_DIR")"
USER_LIST_FILE="$CLAN_ROOT/user-list.nix"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# Unicode symbols
CHECK="✓"
CROSS="✗"
WARNING="⚠"
INFO="ℹ"
ARROW="→"

clear_screen() {
    clear
    echo -e "${BOLD}${BLUE}═══════════════════════════════════════════════════════════════${NC}"
    echo -e "${BOLD}${BLUE}                    TEST USER REMOVAL                         ${NC}"
    echo -e "${BOLD}${BLUE}═══════════════════════════════════════════════════════════════${NC}"
    echo
}

error() {
    echo -e "${RED}${CROSS} ERROR:${NC} $1" >&2
}

info() {
    echo -e "${BLUE}${INFO} INFO:${NC} $1"
}

success() {
    echo -e "${GREEN}${CHECK} SUCCESS:${NC} $1"
}

prompt() {
    echo -ne "${CYAN}${ARROW} $1${NC}"
}

get_users() {
    if [[ ! -f "$USER_LIST_FILE" ]]; then
        echo "No user list file found"
        return 1
    fi

    grep -E '^\s*name\s*=\s*"[^"]+";' "$USER_LIST_FILE" | \
        sed -E 's/.*name\s*=\s*"([^"]+)".*/\1/' | \
        sort
}

main() {
    clear_screen

    info "Getting user list..."

    # Get users into array
    local users_string
    users_string=$(get_users)

    if [[ -z "$users_string" ]]; then
        error "No users found"
        exit 1
    fi

    # Convert to array
    local users=()
    while IFS= read -r line; do
        [[ -n "$line" ]] && users+=("$line")
    done <<< "$users_string"

    success "Found ${#users[@]} users"
    echo

    # Show users
    echo -e "${BOLD}Current Users:${NC}"
    for i in "${!users[@]}"; do
        echo -e "  ${CYAN}[$((i+1))]${NC} ${users[$i]}"
    done
    echo -e "  ${CYAN}[0]${NC} Cancel"
    echo

    # Get selection
    while true; do
        prompt "Select user (0-${#users[@]}): "
        read -r selection

        if [[ "$selection" == "0" ]]; then
            info "Cancelled"
            exit 0
        elif [[ "$selection" =~ ^[1-9][0-9]*$ ]] && [[ $selection -le ${#users[@]} ]]; then
            local selected_user="${users[$((selection-1))]}"
            success "You selected: $selected_user"

            prompt "Confirm removal of '$selected_user'? (y/N): "
            read -r confirm

            if [[ "$confirm" =~ ^[Yy]$ ]]; then
                success "Would remove user: $selected_user"
                exit 0
            else
                info "Cancelled"
                exit 0
            fi
        else
            error "Invalid selection. Please enter 0-${#users[@]}"
        fi
    done
}

main "$@"
