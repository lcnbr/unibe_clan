#!/usr/bin/env bash

# check-uid-conflicts.sh - UID management and conflict detection script
# This script helps prevent UID reuse issues by checking for conflicts

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
    echo "Usage: $0 [OPTIONS]"
    echo
    echo "Check for UID conflicts and suggest safe UID assignments."
    echo
    echo "Options:"
    echo "  -h, --help              Show this help message"
    echo "  -c, --check-conflicts   Check for UID conflicts"
    echo "  -s, --suggest-uid       Suggest next available UID"
    echo "  -l, --list-uids         List all current UIDs"
    echo "  -a, --audit             Full audit of UID usage"
    echo "  --check-orphans         Check for orphaned files/dirs with old UIDs"
    echo
    echo "Examples:"
    echo "  $0 --check-conflicts    # Check for duplicate UIDs"
    echo "  $0 --suggest-uid        # Get next safe UID to use"
    echo "  $0 --audit             # Full UID audit"
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

# Extract UIDs and usernames from user-list.nix
extract_user_data() {
    if [[ ! -f "$USER_LIST_FILE" ]]; then
        error "user-list.nix not found at $USER_LIST_FILE"
    fi

    # Check if we can parse the current file
    if ! nix-instantiate --parse "$USER_LIST_FILE" >/dev/null 2>&1; then
        error "user-list.nix has syntax errors. Please fix before proceeding."
    fi

    # Extract user data using nix-instantiate
    nix-instantiate --eval --expr "
        let
          userData = import $USER_LIST_FILE;
        in
          map (u: { name = u.name; uid = u.uid; }) userData.users
    " 2>/dev/null | sed 's/^\[ //' | sed 's/ \]$//' | tr -d ' ' | sed 's/}{/}\n{/g'
}

check_conflicts() {
    info "Checking for UID conflicts in user-list.nix..."

    local uids_seen=()
    local names_seen=()
    local conflicts=0

    while IFS= read -r user_data; do
        if [[ -n "$user_data" ]]; then
            # Extract name and uid from the nix output
            name=$(echo "$user_data" | sed -n 's/.*name="\([^"]*\)".*/\1/p')
            uid=$(echo "$user_data" | sed -n 's/.*uid=\([0-9]*\).*/\1/p')

            if [[ -n "$name" && -n "$uid" ]]; then
                # Check for duplicate UIDs
                for existing_uid in "${uids_seen[@]}"; do
                    if [[ "$existing_uid" == "$uid" ]]; then
                        warning "Duplicate UID $uid found for user $name"
                        ((conflicts++))
                    fi
                done

                # Check for duplicate names
                for existing_name in "${names_seen[@]}"; do
                    if [[ "$existing_name" == "$name" ]]; then
                        warning "Duplicate username $name found"
                        ((conflicts++))
                    fi
                done

                uids_seen+=("$uid")
                names_seen+=("$name")
            fi
        fi
    done < <(extract_user_data)

    if [[ $conflicts -eq 0 ]]; then
        success "No UID or username conflicts found"
    else
        error "Found $conflicts conflict(s). Please resolve before proceeding."
    fi
}

list_uids() {
    info "Current UID assignments:"
    echo
    printf "%-15s %s\n" "Username" "UID"
    printf "%-15s %s\n" "--------" "---"

    while IFS= read -r user_data; do
        if [[ -n "$user_data" ]]; then
            name=$(echo "$user_data" | sed -n 's/.*name="\([^"]*\)".*/\1/p')
            uid=$(echo "$user_data" | sed -n 's/.*uid=\([0-9]*\).*/\1/p')

            if [[ -n "$name" && -n "$uid" ]]; then
                printf "%-15s %s\n" "$name" "$uid"
            fi
        fi
    done < <(extract_user_data) | sort -k2 -n
}

suggest_uid() {
    info "Analyzing current UIDs to suggest next available..."

    local used_uids=()

    while IFS= read -r user_data; do
        if [[ -n "$user_data" ]]; then
            uid=$(echo "$user_data" | sed -n 's/.*uid=\([0-9]*\).*/\1/p')
            if [[ -n "$uid" ]]; then
                used_uids+=("$uid")
            fi
        fi
    done < <(extract_user_data)

    # Sort UIDs numerically
    IFS=$'\n' used_uids=($(sort -n <<<"${used_uids[*]}"))
    unset IFS

    # Find the highest UID and suggest next
    if [[ ${#used_uids[@]} -gt 0 ]]; then
        highest_uid=${used_uids[-1]}
        suggested_uid=$((highest_uid + 1))

        # Make sure we don't suggest system UIDs (< 1000)
        if [[ $suggested_uid -lt 1000 ]]; then
            suggested_uid=1000
        fi

        success "Suggested next UID: $suggested_uid"
        echo
        info "Current UID range: ${used_uids[0]} - $highest_uid"
        info "Add this to your new user configuration:"
        echo "  uid = $suggested_uid;"
    else
        success "No users found. Suggested starting UID: 1000"
    fi
}

audit_uids() {
    info "Performing full UID audit..."
    echo

    # Check conflicts first
    check_conflicts
    echo

    # List current assignments
    list_uids
    echo

    # Suggest next UID
    suggest_uid
    echo

    # Check for problematic UID ranges
    info "UID range analysis:"
    local system_uids=0
    local user_uids=0

    while IFS= read -r user_data; do
        if [[ -n "$user_data" ]]; then
            uid=$(echo "$user_data" | sed -n 's/.*uid=\([0-9]*\).*/\1/p')
            if [[ -n "$uid" ]]; then
                if [[ $uid -lt 1000 ]]; then
                    ((system_uids++))
                else
                    ((user_uids++))
                fi
            fi
        fi
    done < <(extract_user_data)

    echo "  System UIDs (< 1000): $system_uids"
    echo "  User UIDs (≥ 1000): $user_uids"

    if [[ $system_uids -gt 0 ]]; then
        warning "Found UIDs in system range (< 1000). Consider moving to user range."
    fi
}

check_orphaned_files() {
    info "Checking for potentially orphaned files (requires machine access)..."
    echo
    warning "This check should be run on the target machine, not locally."
    echo
    echo "To check for orphaned files on the machine, run:"
    echo "  # Check for files owned by non-existent users"
    echo "  find /home /shared -nouser 2>/dev/null || true"
    echo
    echo "  # Check for files with specific old UIDs (replace XXXX with old UID)"
    echo "  find /home /shared -uid XXXX 2>/dev/null || true"
    echo
    echo "Common orphaned UID patterns to check:"
    echo "  1109 (alice2's old UID that ben now uses)"
}

# Parse command line arguments
CHECK_CONFLICTS=false
SUGGEST_UID=false
LIST_UIDS=false
AUDIT=false
CHECK_ORPHANS=false

while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            usage
            exit 0
            ;;
        -c|--check-conflicts)
            CHECK_CONFLICTS=true
            shift
            ;;
        -s|--suggest-uid)
            SUGGEST_UID=true
            shift
            ;;
        -l|--list-uids)
            LIST_UIDS=true
            shift
            ;;
        -a|--audit)
            AUDIT=true
            shift
            ;;
        --check-orphans)
            CHECK_ORPHANS=true
            shift
            ;;
        -*)
            error "Unknown option: $1"
            ;;
        *)
            error "Unexpected argument: $1"
            ;;
    esac
done

# Change to clan directory
cd "$CLAN_ROOT"

# Execute requested actions
if [[ "$CHECK_CONFLICTS" == "true" ]]; then
    check_conflicts
elif [[ "$SUGGEST_UID" == "true" ]]; then
    suggest_uid
elif [[ "$LIST_UIDS" == "true" ]]; then
    list_uids
elif [[ "$AUDIT" == "true" ]]; then
    audit_uids
elif [[ "$CHECK_ORPHANS" == "true" ]]; then
    check_orphaned_files
else
    # Default action
    info "No specific action requested. Running audit..."
    echo
    audit_uids
fi
