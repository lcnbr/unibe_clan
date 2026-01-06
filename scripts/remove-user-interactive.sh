#!/usr/bin/env bash

# remove-user-interactive.sh - Interactive user removal script for unibe_clan
# Provides a user-friendly interface for safely removing users

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAN_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
USER_LIST_FILE="$CLAN_ROOT/user-list.nix"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# Unicode symbols
CHECK="✓"
CROSS="✗"
WARNING="⚠"
INFO="ℹ"
ARROW="→"

clear_screen() {
    clear
    echo -e "${BOLD}${BLUE}═══════════════════════════════════════════════════════════════${NC}"
    echo -e "${BOLD}${BLUE}                    UNIBE CLAN USER REMOVAL                    ${NC}"
    echo -e "${BOLD}${BLUE}═══════════════════════════════════════════════════════════════${NC}"
    echo
}

error() {
    echo -e "${RED}${CROSS} ERROR:${NC} $1" >&2
}

warning() {
    echo -e "${YELLOW}${WARNING} WARNING:${NC} $1"
}

info() {
    echo -e "${BLUE}${INFO} INFO:${NC} $1"
}

success() {
    echo -e "${GREEN}${CHECK} SUCCESS:${NC} $1"
}

prompt() {
    echo -e "${CYAN}${ARROW} $1${NC}"
}

# Validation functions
validate_environment() {
    local errors=0

    if [[ ! -f "$USER_LIST_FILE" ]]; then
        error "user-list.nix not found at $USER_LIST_FILE"
        ((errors++))
    fi

    if ! command -v nix-instantiate >/dev/null 2>&1; then
        error "nix-instantiate not found. Please ensure Nix is installed."
        ((errors++))
    fi

    if [[ -f "$USER_LIST_FILE" ]] && ! nix-instantiate --parse "$USER_LIST_FILE" >/dev/null 2>&1; then
        error "user-list.nix has syntax errors. Please fix before proceeding."
        ((errors++))
    fi

    return $errors
}

get_current_users() {
    if [[ ! -f "$USER_LIST_FILE" ]]; then
        return 1
    fi

    grep -E '^\s*name\s*=\s*"[^"]+";' "$USER_LIST_FILE" | sed -E 's/.*name\s*=\s*"([^"]+)".*/\1/' | sort
}

show_user_details() {
    local username="$1"
    local user_found=false

    echo -e "${BOLD}User Details for '$username':${NC}"
    echo "─────────────────────────────────"

    # Find user block and extract details
    local in_user_block=false
    local brace_count=0

    while IFS= read -r line; do
        if [[ "$line" =~ name[[:space:]]*=[[:space:]]*\"$username\" ]]; then
            in_user_block=true
            user_found=true
            echo -e "  ${GREEN}Name:${NC} $username"
        elif [[ "$in_user_block" == "true" ]]; then
            # Count braces to track block boundaries
            local open_braces=$(echo "$line" | grep -o '{' | wc -l || true)
            local close_braces=$(echo "$line" | grep -o '}' | wc -l || true)
            brace_count=$((brace_count + open_braces - close_braces))

            # Extract other user properties
            if [[ "$line" =~ uid[[:space:]]*=[[:space:]]*([0-9]+) ]]; then
                echo -e "  ${GREEN}UID:${NC} ${BASH_REMATCH[1]}"
            elif [[ "$line" =~ description[[:space:]]*=[[:space:]]*\"([^\"]+)\" ]]; then
                echo -e "  ${GREEN}Description:${NC} ${BASH_REMATCH[1]}"
            elif [[ "$line" =~ shell[[:space:]]*=[[:space:]]*([^;]+) ]]; then
                echo -e "  ${GREEN}Shell:${NC} ${BASH_REMATCH[1]}"
            elif [[ "$line" =~ extraGroups[[:space:]]*=[[:space:]]*\[(.*)\] ]]; then
                local groups="${BASH_REMATCH[1]}"
                groups=$(echo "$groups" | sed 's/"//g' | sed 's/[[:space:]]*//g')
                echo -e "  ${GREEN}Extra Groups:${NC} $groups"
            elif [[ "$line" =~ sshKeys[[:space:]]*=[[:space:]]*\[ ]]; then
                echo -e "  ${GREEN}SSH Keys:${NC} [configured]"
            fi

            # End of user block
            if [[ $brace_count -le 0 ]]; then
                break
            fi
        fi
    done < "$USER_LIST_FILE"

    if [[ "$user_found" == "false" ]]; then
        echo -e "  ${RED}User not found in configuration${NC}"
        return 1
    fi

    echo
}

select_user_to_remove() {
    local users=()
    local user_list

    user_list=$(get_current_users)
    if [[ -z "$user_list" ]]; then
        error "No users found in configuration"
        return 1
    fi

    # Convert to array
    while IFS= read -r user; do
        users+=("$user")
    done <<< "$user_list"

    echo -e "${BOLD}Current Users:${NC}"
    echo

    local i=1
    for user in "${users[@]}"; do
        echo -e "  ${CYAN}[$i]${NC} $user"
        ((i++))
    done

    echo -e "  ${CYAN}[0]${NC} Cancel"
    echo

    while true; do
        prompt "Select user to remove (0-${#users[@]}): "
        read -r selection

        if [[ "$selection" == "0" ]]; then
            info "Operation cancelled"
            return 1
        elif [[ "$selection" =~ ^[1-9][0-9]*$ ]] && [[ $selection -le ${#users[@]} ]]; then
            local selected_user="${users[$((selection-1))]}"
            echo
            show_user_details "$selected_user"

            prompt "Remove user '$selected_user'? (y/N): "
            read -r confirm

            if [[ "$confirm" =~ ^[Yy]$ ]]; then
                echo "$selected_user"
                return 0
            else
                echo
                warning "User removal cancelled"
                return 1
            fi
        else
            warning "Invalid selection. Please enter a number between 0 and ${#users[@]}."
        fi
    done
}

ask_backup_options() {
    local create_backup=false
    local backup_file=""

    echo -e "${BOLD}Backup Options:${NC}"
    echo
    prompt "Create backup of user-list.nix before changes? (Y/n): "
    read -r backup_response

    if [[ ! "$backup_response" =~ ^[Nn]$ ]]; then
        create_backup=true

        local default_backup="user-list.backup.$(date +%Y%m%d-%H%M%S).nix"
        prompt "Backup filename (default: $default_backup): "
        read -r backup_input

        if [[ -z "$backup_input" ]]; then
            backup_file="$default_backup"
        else
            backup_file="$backup_input"
        fi

        # Ensure .nix extension
        if [[ ! "$backup_file" =~ \.nix$ ]]; then
            backup_file="${backup_file}.nix"
        fi
    fi

    echo "$create_backup|$backup_file"
}

show_removal_plan() {
    local username="$1"
    local backup_file="$2"

    echo -e "${BOLD}Removal Plan:${NC}"
    echo "═══════════════"
    echo
    echo -e "${BLUE}1.${NC} Remove user '$username' from user-list.nix"

    if [[ -n "$backup_file" ]]; then
        echo -e "${BLUE}2.${NC} Create backup: $backup_file"
    fi

    echo -e "${BLUE}3.${NC} Validate resulting configuration"
    echo
    echo -e "${BOLD}When deployed to machines:${NC}"
    echo -e "${BLUE}4.${NC} Create ZFS backup snapshots of user data"
    echo -e "${BLUE}5.${NC} Copy user data to /shared/deleted-users/"
    echo -e "${BLUE}6.${NC} Remove user's ZFS dataset"
    echo -e "${BLUE}7.${NC} Remove system user account"
    echo -e "${BLUE}8.${NC} Clean up user directories in /shared/"
    echo
    warning "This action cannot be easily undone!"
    warning "User data will be preserved in backup snapshots and /shared/deleted-users/"
    echo
}

perform_removal() {
    local username="$1"
    local backup_file="$2"

    # Create backup if requested
    if [[ -n "$backup_file" ]]; then
        info "Creating backup: $backup_file"
        if cp "$USER_LIST_FILE" "$backup_file"; then
            success "Backup created successfully"
        else
            error "Failed to create backup"
            return 1
        fi
    fi

    # Find user block boundaries
    local user_start_line
    user_start_line=$(grep -n "name = \"$username\";" "$USER_LIST_FILE" | cut -d: -f1)

    if [[ -z "$user_start_line" ]]; then
        error "Could not locate user block for '$username'"
        return 1
    fi

    # Find the opening brace before the name line
    local block_start_line=$user_start_line
    while [[ $block_start_line -gt 1 ]]; do
        if grep -q "^\s*{" <(sed -n "${block_start_line}p" "$USER_LIST_FILE"); then
            break
        fi
        ((block_start_line--))
    done

    # Find the closing brace after the name line
    local block_end_line=$user_start_line
    local total_lines
    total_lines=$(wc -l < "$USER_LIST_FILE")
    local brace_count=0

    while [[ $block_end_line -le $total_lines ]]; do
        local line
        line=$(sed -n "${block_end_line}p" "$USER_LIST_FILE")
        # Count opening braces
        brace_count=$((brace_count + $(echo "$line" | grep -o '{' | wc -l || echo 0)))
        # Count closing braces
        brace_count=$((brace_count - $(echo "$line" | grep -o '}' | wc -l || echo 0)))

        # If we've closed all braces, we found the end
        if [[ $brace_count -eq 0 && $block_end_line -gt $user_start_line ]]; then
            break
        fi
        ((block_end_line++))
    done

    info "Removing lines $block_start_line to $block_end_line from user-list.nix"

    # Create temporary file with user removed
    local temp_file
    temp_file=$(mktemp)

    if ! sed "${block_start_line},${block_end_line}d" "$USER_LIST_FILE" > "$temp_file"; then
        rm -f "$temp_file"
        error "Failed to create modified configuration"
        return 1
    fi

    # Validate the result
    info "Validating resulting configuration..."
    if ! nix-instantiate --parse "$temp_file" >/dev/null 2>&1; then
        rm -f "$temp_file"
        error "Resulting configuration would be invalid. Aborting."
        return 1
    fi

    success "Configuration validation passed"

    # Apply the changes
    if mv "$temp_file" "$USER_LIST_FILE"; then
        success "User '$username' removed from configuration"
    else
        error "Failed to save changes"
        return 1
    fi

    return 0
}

show_next_steps() {
    echo
    echo -e "${BOLD}Next Steps:${NC}"
    echo "═══════════════"
    echo
    echo -e "${BLUE}1.${NC} Review the changes:"
    echo -e "   ${CYAN}git diff user-list.nix${NC}"
    echo
    echo -e "${BLUE}2.${NC} Deploy to machines:"
    echo -e "   ${CYAN}clan machines update <machine-name>${NC}"
    echo
    echo -e "${BLUE}3.${NC} Verify user data backups:"
    echo -e "   ${CYAN}ssh <machine> 'zfs list -t snapshot | grep deleted'${NC}"
    echo -e "   ${CYAN}ssh <machine> 'ls -la /shared/deleted-users/'${NC}"
    echo
    warning "Remember: ZFS snapshots will be cleaned up automatically after 30 days"
    info "File backups in /shared/deleted-users/ are permanent until manually removed"
}

main() {
    # Change to clan directory
    cd "$CLAN_ROOT"

    clear_screen

    info "Validating environment..."
    if ! validate_environment; then
        echo
        error "Environment validation failed. Please fix the issues above."
        exit 1
    fi
    success "Environment validation passed"
    echo

    # Select user
    info "Selecting user to remove..."
    local username
    if ! username=$(select_user_to_remove); then
        exit 1
    fi

    clear_screen
    echo -e "${BOLD}Removing User: ${CYAN}$username${NC}"
    echo

    # Backup options
    local backup_options
    backup_options=$(ask_backup_options)
    local create_backup="${backup_options%%|*}"
    local backup_file="${backup_options##*|}"

    if [[ "$create_backup" == "false" ]]; then
        backup_file=""
    fi

    echo

    # Show plan
    show_removal_plan "$username" "$backup_file"

    # Final confirmation
    prompt "Proceed with user removal? (y/N): "
    read -r final_confirm

    if [[ ! "$final_confirm" =~ ^[Yy]$ ]]; then
        echo
        warning "User removal cancelled by user"
        exit 0
    fi

    echo
    info "Performing user removal..."

    if perform_removal "$username" "$backup_file"; then
        echo
        success "User removal completed successfully!"
        show_next_steps
    else
        echo
        error "User removal failed"
        exit 1
    fi
}

# Handle Ctrl+C gracefully
trap 'echo; warning "Operation interrupted by user"; exit 130' INT

main "$@"
