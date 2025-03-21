{
  lib,
  clan-core,
  #config,
  ...
}: let
  userData = import ../user-list.nix;
in {
  imports = [
    clan-core.clanModules.disk-id
  ];

  # DO NOT EDIT THIS FILE AFTER INSTALLATION of a machine
  # Otherwise your system might not boot because of missing partitions / filesystems
  # boot.loader.grub.efiSupport = lib.mkDefault true;
  boot.loader.systemd-boot.enable = true;
  # boot.loader.grub.efiInstallAsRemovable = lib.mkDefault true;
  disko.devices = {
    disk = {
      "main" = {
        # suffix is to prevent disk name collisions
        name = "main-"; # + suffix;
        type = "disk";
        # Set the following in flake.nix for each maschine:
        # device = <uuid>;

        content = {
          type = "gpt";
          partitions = {
            ESP = {
              size = "1G";
              type = "EF00";
              content = {
                type = "filesystem";
                format = "vfat";
                mountpoint = "/boot";
                mountOptions = ["umask=0077"];
              };
            };
            zfs = {
              size = "100%";
              content = {
                type = "zfs";
                pool = "zroot";
              };
            };
          };
        };
      };
    };
    zpool = {
      zroot = {
        type = "zpool";
        options.ashift = "12";
        rootFsOptions = {
          # https://wiki.archlinux.org/title/Install_Arch_Linux_on_ZFS
          acltype = "posixacl";
          atime = "off";
          compression = "zstd";
          mountpoint = "none";
          xattr = "sa";
        };

        datasets =
          {
            "local" = {
              type = "zfs_fs";
            };

            "local/home" = {
              type = "zfs_fs";
              mountpoint = "/home";
              # any shared properties for the "home" dataset
            };

            "local/nix" = {
              type = "zfs_fs";
              mountpoint = "/nix";
              options."com.sun:auto-snapshot" = "false";
            };
            "local/root" = {
              type = "zfs_fs";
              mountpoint = "/";
              options."com.sun:auto-snapshot" = "false";
            };

            # Add the shared dataset
            "local/shared" = {
                  type = "zfs_fs";
                  mountpoint = "/shared";
                  options = {
                    "com.sun:auto-snapshot" = "true";
                  };
                  # postCreateHook = ''
                  #   # Create base shared directory
                  #   mkdir -p /shared
                  #   chmod 755 /shared

                  #   # Create and set permissions for each user's directory
                  #   ${builtins.concatStringsSep "\n" (map (user: ''
                  #     mkdir -p /shared/${user.name}
                  #     chown ${user.name}:users /shared/${user.name}
                  #     chmod 750 /shared/${user.name}

                  #     # Set ACLs for the user directory
                  #     setfacl -m u:${user.name}:rwx /shared/${user.name}
                  #     setfacl -m g:users:rx /shared/${user.name}

                  #     # Set default ACLs for new files/directories
                  #     setfacl -d -m u:${user.name}:rwx /shared/${user.name}
                  #     setfacl -d -m g:users:rx /shared/${user.name}
                  #   '') userData.users)}
                  # '';
                };

            # 3) Then for each user, create a child dataset named local/home/<username>.
            #    Since parted-based disk config has no "children" block,
            #    we must define each as a separate entry in `datasets`.
          }
          # merges with the definitions above
          // lib.genAttrs
          (map (u: u.name) userData.users)
          (
            userName: let
              userSpec = lib.findFirst (u: u.name == userName) userData.users;
            in {
              # each attribute is named "local/home/alice", etc.
              name = "local/home/${userName}";

              type = "zfs_fs";
              mountpoint = "/home/${userName}";
              options = {
                refquota = "50G";
                compression = "lz4";
              };
            }
          );
      };
    };
  };
}
