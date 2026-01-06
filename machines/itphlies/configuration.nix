{
  lib,
  pkgs,
  ...
}: let
  userData = import ../../user-list.nix; # The file above
in {
  imports = [
    # contains your disk format and partitioning configuration.
    ../../modules/user-disko.nix
    ../../modules/shared.nix
  ];

  users.users =
    lib.genAttrs
    (map (u: u.name) userData.users)
    (userName: let
      userSpec = lib.findFirst (u: u.name == userName) null userData.users;
    in {
      isNormalUser = true;
      uid = userSpec.uid;
      shell = userSpec.shell or null;
      extraGroups = (userSpec.extraGroups or []) ++ ["users" "wheel"];
      home = "/home/${userName}";
      description = userSpec.description or "";
      openssh.authorizedKeys.keys = userSpec.sshKeys or [];
    });

  # Ensure the directory structure persists
  systemd.tmpfiles.rules = let
    userDirRules = builtins.concatStringsSep "\n" (map (user: ''
        d /shared/${user.name} 0750 ${user.name} users -
      '')
      userData.users);
  in [
    "d /shared 0755 root root -"
    "d /shared/deleted-users 0700 root wheel -"
    userDirRules
  ];

  # Activation script to clean up orphaned shared directories immediately during deployment
  system.activationScripts.cleanupOrphanedSharedDirs = {
    text = ''
      echo "Checking for orphaned shared directories during activation..."

      # Get current list of configured users
      CONFIGURED_USERS=(${builtins.concatStringsSep " " (map (u: u.name) userData.users)})

      # Check existing shared directories
      if [[ -d /shared ]]; then
        for shared_dir in /shared/*/; do
          if [[ -d "$shared_dir" ]]; then
            dir_name=$(basename "$shared_dir")

            # Skip special directories
            if [[ "$dir_name" == "deleted-users" ]]; then
              continue
            fi

            # Check if this user is still configured
            user_found=false
            for configured_user in "''${CONFIGURED_USERS[@]}"; do
              if [[ "$dir_name" == "$configured_user" ]]; then
                user_found=true
                break
              fi
            done

            # If user not found in configuration, clean up their shared directory
            if [[ "$user_found" == "false" ]]; then
              echo "Activation cleanup: Orphaned shared directory found: /shared/$dir_name"

              TIMESTAMP=$(date +%Y%m%d-%H%M%S)

              # Create backup of shared directory content if it exists and has files
              if [[ "$(ls -A "/shared/$dir_name" 2>/dev/null)" ]]; then
                SHARED_BACKUP_DIR="/shared/deleted-users/$dir_name-shared-$TIMESTAMP"
                echo "Activation cleanup: Backing up shared directory content to $SHARED_BACKUP_DIR"
                if mkdir -p "$SHARED_BACKUP_DIR" 2>/dev/null; then
                  ${pkgs.rsync}/bin/rsync -av "/shared/$dir_name/" "$SHARED_BACKUP_DIR/" || echo "Shared directory backup failed"
                  chown -R root:wheel "$SHARED_BACKUP_DIR" 2>/dev/null || true
                  chmod -R 700 "$SHARED_BACKUP_DIR" 2>/dev/null || true
                fi
              fi

              # Remove the shared directory
              rm -rf "/shared/$dir_name" || echo "Warning: Could not remove shared directory /shared/$dir_name"
              echo "Activation cleanup: Successfully removed orphaned shared directory for user $dir_name"
            fi
          fi
        done
      fi

      echo "Activation cleanup of shared directories completed"
    '';
    deps = ["users" "groups"];
  };

  # Shared directory cleanup service for deleted users
  systemd.services.cleanup-shared-directories = {
    description = "Cleanup shared directories for deleted users";
    wantedBy = ["multi-user.target"];
    after = ["local-fs.target"];
    # Run on every system activation to ensure cleanup happens
    restartIfChanged = true;

    script = ''
      echo "Checking for orphaned shared directories..."

      # Get current list of configured users
      CONFIGURED_USERS=(${builtins.concatStringsSep " " (map (u: u.name) userData.users)})

      # Check existing shared directories
      if [[ -d /shared ]]; then
        for shared_dir in /shared/*/; do
          if [[ -d "$shared_dir" ]]; then
            dir_name=$(basename "$shared_dir")

            # Skip special directories
            if [[ "$dir_name" == "deleted-users" ]]; then
              continue
            fi

            # Check if this user is still configured
            user_found=false
            for configured_user in "''${CONFIGURED_USERS[@]}"; do
              if [[ "$dir_name" == "$configured_user" ]]; then
                user_found=true
                break
              fi
            done

            # If user not found in configuration, clean up their shared directory
            if [[ "$user_found" == "false" ]]; then
              echo "User $dir_name is no longer configured, cleaning up shared directory..."

              TIMESTAMP=$(date +%Y%m%d-%H%M%S)

              # Create backup of shared directory content if it exists and has files
              if [[ "$(ls -A "/shared/$dir_name" 2>/dev/null)" ]]; then
                SHARED_BACKUP_DIR="/shared/deleted-users/$dir_name-shared-$TIMESTAMP"
                echo "Backing up shared directory content to $SHARED_BACKUP_DIR"
                if mkdir -p "$SHARED_BACKUP_DIR" 2>/dev/null; then
                  ${pkgs.rsync}/bin/rsync -av "/shared/$dir_name/" "$SHARED_BACKUP_DIR/" || echo "Shared directory backup failed"
                  chown -R root:wheel "$SHARED_BACKUP_DIR" 2>/dev/null || true
                  chmod -R 700 "$SHARED_BACKUP_DIR" 2>/dev/null || true
                fi
              fi

              # Remove the shared directory
              rm -rf "/shared/$dir_name" || echo "Warning: Could not remove shared directory /shared/$dir_name"
              echo "Successfully removed shared directory for user $dir_name"
            fi
          fi
        done
      fi

      echo "Shared directory cleanup completed"
    '';

    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      User = "root";
    };
  };

  # Cleanup old file backups in /shared/deleted-users (older than 90 days)
  systemd.services.cleanup-deleted-user-files = {
    description = "Cleanup old deleted-user file backups";
    script = ''
      echo "Cleaning up deleted-user file backups older than 90 days..."

      if [[ -d /shared/deleted-users ]]; then
        # Find and remove directories older than 90 days
        find /shared/deleted-users -maxdepth 1 -type d -name "*-20[0-9][0-9][0-9][0-9][0-9][0-9]-*" -mtime +90 -exec rm -rf {} \; -print | while read -r dir; do
          echo "Removed old backup directory: $dir"
        done
      fi

      echo "File backup cleanup completed"
    '';
    serviceConfig = {
      Type = "oneshot";
      User = "root";
    };
  };

  # Run file backup cleanup weekly on Sunday at 3 AM
  systemd.timers.cleanup-deleted-user-files = {
    wantedBy = ["timers.target"];
    timerConfig = {
      OnCalendar = "Sun *-*-* 03:00:00";
      RandomizedDelaySec = "1h";
      Persistent = true;
    };
  };

  users.groups = userData.groups;

  services.openssh.enable = true;
  services.openssh.settings.PasswordAuthentication = false;
  services.openssh.settings.PermitRootLogin = "no";
  security.sudo.enable = true;
  security.sudo.wheelNeedsPassword = false;
  clan.core.networking.targetHost = "lcnbr@130.92.184.209";

  security.sudo.execWheelOnly = true;
  networking.hostName = "itphlies";
  networking.hostId = "a1a034da";
  networking.interfaces.eno1np0.ipv4.addresses = [
    {
      address = "130.92.184.209";
      prefixLength = 24;
    }
  ];

  networking.defaultGateway.interface = "eno1np0";

  networking.defaultGateway.address = "130.92.184.1";
  networking.nameservers = ["130.92.9.52" "130.92.9.53"];

  boot.initrd.systemd.enable = true;
  # boot.supportedFilesystems = [ "zfs" ];
  # boot.zfs.devNodes = "/dev/disk/by-id";
  # boot.zfs.forceImportRoot = false;
  boot.initrd.systemd.emergencyAccess = true;

  programs.nix-ld.enable = true;
  environment.systemPackages = [
    pkgs.ipmitool
    pkgs.rsync
  ];

  # Set mercury as the default user for local login and emergency console
  services.getty.autologinUser = "mercury";

  disko.devices.disk.main.device = "/dev/disk/by-id/nvme-WUS5EA1A1ESP5E3_240420800175";

  system.stateVersion = "25.05";
}
