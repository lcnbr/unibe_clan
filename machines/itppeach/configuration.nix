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
      shell = userSpec.shell or pkgs.fish;
      extraGroups = (userSpec.extraGroups or []) ++ ["users" "wheel"];
      home = "/home/${userName}";
      description = userSpec.description or "";
      openssh.authorizedKeys.keys = userSpec.sshKeys or [];
      # Enable autologin for mercury by setting empty password
      hashedPassword =
        if userName == "mercury"
        then ""
        else null;
    });

  # Ensure the directory structure persists
  systemd.tmpfiles.rules = let
    userDirRules = builtins.concatStringsSep "\n" (map (user: ''
        d /shared/${user.name} 0750 ${user.name} users -
      '')
      userData.users);
    # Note: User home directories will be created as ZFS datasets by activation script
  in [
    "d /shared 0755 root root -"
    "d /shared/deleted-users 0700 root wheel -"
    userDirRules
  ];

  # ZFS user dataset management service
  systemd.services.zfs-user-datasets = {
    description = "Manage ZFS datasets for users";
    wantedBy = ["multi-user.target"];
    after = ["zfs-import.target" "local-fs.target"];
    wants = ["zfs-import.target"];

    script = ''
      # Check if ZFS pool exists
      if ${pkgs.zfs}/bin/zfs list zroot/local/home >/dev/null 2>&1; then
        echo "Managing per-user ZFS datasets..."

        # Get current list of configured users
        CONFIGURED_USERS=(${builtins.concatStringsSep " " (map (u: u.name) userData.users)})

        # Get existing user datasets
        EXISTING_DATASETS=$(${pkgs.zfs}/bin/zfs list -H -o name -r zroot/local/home | grep "^zroot/local/home/[^/]*$" | sed 's|zroot/local/home/||' || true)

        # Create datasets for new users
        ${builtins.concatStringsSep "\n" (map (user: ''
          # Check if user exists in system before proceeding
          if ! id ${user.name} >/dev/null 2>&1; then
            echo "User ${user.name} does not exist yet, skipping dataset creation"
            continue
          fi

          if ! ${pkgs.zfs}/bin/zfs list zroot/local/home/${user.name} >/dev/null 2>&1; then
            echo "Creating ZFS dataset for user ${user.name}..."
            ${pkgs.zfs}/bin/zfs create -o refquota=50G -o compression=lz4 -o mountpoint=/home/${user.name} zroot/local/home/${user.name} || {
              echo "Failed to create dataset for ${user.name}"
              continue
            }

            # Wait for mount to complete
            sleep 2
          fi

          # Always fix permissions (for both new and existing datasets)
          echo "Setting proper ownership and permissions for ${user.name}..."

          # Set home directory ownership and permissions
          chown ${user.name}:users /home/${user.name} 2>/dev/null || echo "Warning: Could not set ownership for /home/${user.name}"
          chmod 755 /home/${user.name} 2>/dev/null || echo "Warning: Could not set permissions for /home/${user.name}"

          # Create essential directories with proper ownership
          mkdir -p /home/${user.name}/.config /home/${user.name}/.local/share /home/${user.name}/.cache
          chown -R ${user.name}:users /home/${user.name}/.config /home/${user.name}/.local /home/${user.name}/.cache 2>/dev/null || echo "Warning: Could not set ownership for user directories"
          chmod -R 755 /home/${user.name}/.config /home/${user.name}/.local /home/${user.name}/.cache 2>/dev/null || echo "Warning: Could not set permissions for user directories"

          # Fix any existing files that might be owned by root
          find /home/${user.name} -user root -exec chown ${user.name}:users {} \; 2>/dev/null || true
        '')
        userData.users)}

        # Handle user deletion - backup and remove datasets for users no longer configured
        for existing_user in $EXISTING_DATASETS; do
          user_found=false
          for configured_user in "''${CONFIGURED_USERS[@]}"; do
            if [ "$existing_user" = "$configured_user" ]; then
              user_found=true
              break
            fi
          done

          if [ "$user_found" = "false" ] && [ -n "$existing_user" ]; then
            echo "User $existing_user is no longer configured, handling cleanup..."

            # Create backup snapshot before deletion
            TIMESTAMP=$(date +%Y%m%d-%H%M%S)
            SNAPSHOT_NAME="zroot/local/home/$existing_user@deleted-$TIMESTAMP"

            echo "Creating backup snapshot: $SNAPSHOT_NAME"
            if ${pkgs.zfs}/bin/zfs snapshot "$SNAPSHOT_NAME"; then
              echo "Backup snapshot created successfully"

              # Optional: Export user data to /shared/deleted-users for additional backup
              BACKUP_DIR="/shared/deleted-users/$existing_user-$TIMESTAMP"
              if mkdir -p "$BACKUP_DIR" 2>/dev/null; then
                echo "Creating additional backup in $BACKUP_DIR"
                ${pkgs.rsync}/bin/rsync -av "/home/$existing_user/" "$BACKUP_DIR/" || echo "Additional backup failed, but snapshot exists"
                chown -R root:wheel "$BACKUP_DIR" 2>/dev/null || true
                chmod -R 700 "$BACKUP_DIR" 2>/dev/null || true
              fi

              # Unmount and destroy the dataset
              echo "Removing ZFS dataset for deleted user: $existing_user"
              ${pkgs.zfs}/bin/zfs unmount "zroot/local/home/$existing_user" 2>/dev/null || true
              if ${pkgs.zfs}/bin/zfs destroy "zroot/local/home/$existing_user"; then
                echo "Successfully removed dataset for user $existing_user"
                echo "Backup snapshot retained: $SNAPSHOT_NAME"
              else
                echo "Failed to destroy dataset for user $existing_user"
              fi
            else
              echo "Failed to create backup snapshot, keeping dataset for safety"
            fi
          fi
        done
      else
        echo "ZFS dataset zroot/local/home not found, skipping dataset management"
      fi
    '';

    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      User = "root";
    };
  };

  # Automatic cleanup of old deleted-user snapshots (older than 30 days)
  systemd.services.cleanup-deleted-user-snapshots = {
    description = "Cleanup old deleted-user ZFS snapshots";
    script = ''
      echo "Cleaning up deleted-user snapshots older than 30 days..."

      # Find and destroy snapshots older than 30 days
      CUTOFF_DATE=$(date -d '30 days ago' +%Y%m%d)

      ${pkgs.zfs}/bin/zfs list -H -o name -t snapshot | grep '@deleted-' | while read -r snapshot; do
        # Extract timestamp from snapshot name (format: @deleted-YYYYMMDD-HHMMSS)
        if [[ "$snapshot" =~ @deleted-([0-9]{8})-[0-9]{6}$ ]]; then
          SNAPSHOT_DATE="''${BASH_REMATCH[1]}"

          # Compare dates (simple string comparison works for YYYYMMDD format)
          if [[ "$SNAPSHOT_DATE" < "$CUTOFF_DATE" ]]; then
            echo "Destroying old snapshot: $snapshot"
            ${pkgs.zfs}/bin/zfs destroy "$snapshot" || echo "Failed to destroy $snapshot"
          fi
        fi
      done

      echo "Snapshot cleanup completed"
    '';
    serviceConfig = {
      Type = "oneshot";
      User = "root";
    };
  };

  # Run snapshot cleanup daily at 2 AM
  systemd.timers.cleanup-deleted-user-snapshots = {
    wantedBy = ["timers.target"];
    timerConfig = {
      OnCalendar = "daily";
      RandomizedDelaySec = "1h";
      Persistent = true;
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
  clan.core.networking.targetHost = "lcnbr@130.92.184.229";

  security.sudo.execWheelOnly = true;
  networking.hostName = "itppeach";
  networking.hostId = "aaaa3453";
  networking.interfaces.enp7s0.ipv4.addresses = [
    {
      address = "130.92.184.229";
      prefixLength = 24;
    }
  ];

  networking.defaultGateway.interface = "enp7s0";

  networking.defaultGateway.address = "130.92.184.1";
  networking.nameservers = ["130.92.9.52" "130.92.9.53"];

  boot.initrd.systemd.enable = true;
  boot.supportedFilesystems = ["zfs"];
  boot.zfs.devNodes = "/dev/disk/by-id";
  boot.zfs.forceImportRoot = false;
  boot.initrd.systemd.emergencyAccess = true;

  programs.nix-ld.enable = true;
  environment.systemPackages = [
    pkgs.ipmitool
    pkgs.zfs
    pkgs.rsync
  ];

  # Set mercury as the default user for local login and emergency console
  services.getty.autologinUser = "mercury";

  disko.devices.disk.main.device = "/dev/disk/by-id/wwn-0x55cd2e41503ed9cb";

  system.stateVersion = "25.05";
}
