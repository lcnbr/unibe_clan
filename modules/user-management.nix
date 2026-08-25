{
  config,
  pkgs,
  lib,
  ...
}: let
  userData = import config.unibe.userListFile;
in {
  # This module sets up Home Manager configurations for all users
  # User creation is handled by machine configurations

  # Create activation script that runs after user creation
  system.activationScripts.setupUserHomes = {
    text = ''
      echo "Setting up Home Manager configurations for all users..."

      ${lib.concatMapStringsSep "\n" (userSpec: ''
                    USER_NAME="${userSpec.name}"
                    USER_HOME="/home/$USER_NAME"
                    HM_CONFIG_DIR="$USER_HOME/.config/home-manager"

                    # Only create if user home exists and config doesn't exist yet
                    if [ -d "$USER_HOME" ] && [ ! -f "$HM_CONFIG_DIR/home.nix" ]; then
                      echo "Creating Home Manager config for $USER_NAME..."
                      echo "DEBUG: USER_HOME=$USER_HOME, HM_CONFIG_DIR=$HM_CONFIG_DIR"
                      echo "DEBUG: Running as user: $(whoami)"
                      echo "DEBUG: User $USER_NAME exists: $(id $USER_NAME 2>/dev/null && echo 'YES' || echo 'NO')"

                      # Create the config directory
                      mkdir -p "$HM_CONFIG_DIR"

                      # Copy template and substitute username placeholder using sed
                      echo "DEBUG: Creating home.nix for $USER_NAME"

                      # Ensure sed is available by using full path
                      SED_PATH="${pkgs.gnused}/bin/sed"

                      if [ -r /etc/home-manager-templates/default-standalone-home.nix ]; then
                        if "$SED_PATH" "s/USERNAME_PLACEHOLDER/$USER_NAME/g" /etc/home-manager-templates/default-standalone-home.nix > "$HM_CONFIG_DIR/home.nix"; then
                          echo "DEBUG: Successfully created home.nix ($(wc -l < "$HM_CONFIG_DIR/home.nix") lines)"

                          # Verify the file has content
                          if [ ! -s "$HM_CONFIG_DIR/home.nix" ]; then
                            echo "ERROR: home.nix is empty after sed substitution!"
                          fi
                        else
                          echo "ERROR: sed command failed for $USER_NAME"
                        fi
                      else
                        echo "ERROR: Template file not readable"
                        echo "Template file exists: $(test -f /etc/home-manager-templates/default-standalone-home.nix && echo YES || echo NO)"
                        echo "Template readable: $(test -r /etc/home-manager-templates/default-standalone-home.nix && echo YES || echo NO)"
                      fi

                      # Create flake.nix
                      cat > "$HM_CONFIG_DIR/flake.nix" << EOF
          {
            description = "Home Manager configuration for ${userSpec.name}";

            inputs = {
              nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
              home-manager.url = "github:nix-community/home-manager";
              home-manager.inputs.nixpkgs.follows = "nixpkgs";
            };

            outputs = { nixpkgs, home-manager, ... }: {
              homeConfigurations."${userSpec.name}" = home-manager.lib.homeManagerConfiguration {
                pkgs = nixpkgs.legacyPackages.x86_64-linux;
                modules = [ ./home.nix ];
              };
            };
          }
          EOF

                      # Create .gitignore
                      cat > "$HM_CONFIG_DIR/.gitignore" << 'EOF'
          result
          result-*
          EOF

                      # Set proper ownership and permissions with better error handling
                      echo "DEBUG: About to set ownership for ${userSpec.name}"
                      echo "DEBUG: Files before chown: $(ls -la $HM_CONFIG_DIR/)"

                      if id "${userSpec.name}" >/dev/null 2>&1; then
                        echo "DEBUG: User ${userSpec.name} exists, setting ownership"
                        # Try user:user first, then user:users as fallback
                        if chown -R ${userSpec.name}:${userSpec.name} "$HM_CONFIG_DIR" 2>/dev/null; then
                          echo "DEBUG: Successfully set ownership to ${userSpec.name}:${userSpec.name}"
                        elif chown -R ${userSpec.name}:users "$HM_CONFIG_DIR" 2>/dev/null; then
                          echo "DEBUG: Successfully set ownership to ${userSpec.name}:users"
                        else
                          echo "ERROR: Failed to set ownership for ${userSpec.name}"
                        fi

                        chmod 755 "$HM_CONFIG_DIR"
                        chmod 644 "$HM_CONFIG_DIR"/* 2>/dev/null || true
                        echo "DEBUG: Files after chown: $(ls -la $HM_CONFIG_DIR/)"
                      else
                        echo "Warning: User ${userSpec.name} does not exist yet, skipping ownership change"
                      fi

                      echo "✅ Created Home Manager config for ${userSpec.name}"
                    fi
        '')
        userData.users}

      echo "Home Manager setup complete!"
    '';
    deps = ["users"];
  };

  # Removed profile.d script to prevent duplication - using only bash interactiveShellInit

  # Create bashrc hook for interactive shells
  programs.bash.interactiveShellInit = ''
    # Show Home Manager greeting if config exists but not activated
    if [[ -f ~/.config/home-manager/home.nix ]]; then
      if ! home-manager generations &>/dev/null || [[ "$(home-manager generations 2>/dev/null | wc -l)" -eq 0 ]]; then
        echo ""
        sed "s/\$(hostname)/$(hostname)/g; s/\$(whoami)/$(whoami)/g" /etc/home-manager-templates/greeting.txt
        echo ""
      fi
    fi
  '';

  # Also create Fish-specific greeting
  programs.fish.interactiveShellInit = ''
    # Override fish_greeting function to replace default Fish welcome message
    if test -f ~/.config/home-manager/home.nix
      # Show Home Manager greeting if config exists but not activated
      if not home-manager generations >/dev/null 2>&1; or test (home-manager generations 2>/dev/null | wc -l) -eq 0
        function fish_greeting
          echo ""
          sed "s/\\\$(hostname)/"(hostname)"/g; s/\\\$(whoami)/"(whoami)"/g" /etc/home-manager-templates/greeting.txt
          echo ""
        end
      end
    end
  '';

  # Universal MOTD as fallback - shows same message as interactive greeting
  environment.etc."motd".source = ../home-manager-templates/greeting.txt;

  # Make sure home-manager is available for users
  environment.systemPackages = with pkgs; [
    home-manager
    # Debug script to check greeting conditions
    (pkgs.writeShellScriptBin "check-hm-greeting" ''
      echo "=== Home Manager Greeting Debug ==="
      echo "User: $(whoami)"
      echo "Home: $HOME"
      echo "Shell: $SHELL"
      echo ""

      if [ -f ~/.config/home-manager/home.nix ]; then
        echo "✅ Home Manager config exists"
      else
        echo "❌ Home Manager config missing"
      fi

      if home-manager generations &>/dev/null && [[ "$(home-manager generations 2>/dev/null | wc -l)" -gt 0 ]]; then
        echo "✅ Home Manager is activated ($(home-manager generations | wc -l) generations)"
      else
        echo "❌ Home Manager not activated yet"
      fi

      echo ""
      if [[ -f ~/.config/home-manager/home.nix ]] && ! home-manager generations &>/dev/null; then
        echo "Greeting should show: YES"
      else
        echo "Greeting should show: NO"
      fi
      echo ""
      echo "To manually test the greeting logic:"
      echo "  Fish: source /etc/fish/config.fish"
      echo "  Bash: Start a new bash session (no profile.d script anymore)"
      echo "  Debug: The greeting uses bash interactiveShellInit only"
    '')
  ];

  # Enable fish shell system-wide (users can override in their home-manager config)
  programs.fish.enable = true;
  programs.bash.enable = true;
}
