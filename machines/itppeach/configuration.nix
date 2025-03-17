{
  lib,
  pkgs,
  ...
}: {
  imports = [
    # contains your disk format and partitioning configuration.
    ../../modules/disko.nix
    # this file is shared among all machines
    ../../modules/shared.nix
    # enables GNOME desktop (optional)
    # ../../modules/gnome.nix
    # ../../root-passwd.nix
  ];

  # This is your user login name.
  # users.users.user.name = "lcnbr";

  # Set this for clan commands use ssh i.e. `clan machines update`
  # If you change the hostname, you need to update this line to root@<new-hostname>
  # This only works however if you have avahi running on your admin machine else use IP
  clan.core.networking.targetHost = "root@130.92.184.229";

  networking.hostName = "itppeach";
  networking.hostId = "aaaa3453";
  networking.interfaces.enp7s0.ipv4.addresses = [
    {
      address = "130.92.184.229";
      prefixLength = 24;
    }
  ];

  networking.defaultGateway.interface = "enp7s0";

  # networking.defaultGateway.interface = "wlp170s0";
  networking.defaultGateway.address = "130.92.184.1";
  networking.nameservers = ["130.92.9.52" "130.92.9.53"];

  fileSystems."/persist" = {
    neededForBoot = true;
    device = "zroot/local/persist";
    fsType = "zfs";
  };

  # boot.initrd.postResumeCommands = lib.mkAfter ''
  #    zfs rollback -r zroot/local/root@blank && echo "rollback complete"
  #  '';

  boot.initrd.systemd.enable = true;

  # boot.initrd.systemd.services.reset = {
  #     description = "reset root filesystem";
  #     wantedBy = [ "initrd.target" ];
  #     after = [ "zfs-import-zroot.service" ];
  #     before = [ "sysroot.mount" ];
  #     path = with pkgs; [ zfs ];
  #     unitConfig.DefaultDependencies = "no";
  #     serviceConfig.Type = "oneshot";
  #     script = ''
  #         zfs rollback -r zroot/local/root@blank'';
  #   };
  users.users.lcnbr = {
    isNormalUser = true;
    extraGroups = [
      "wheel"
      "networkmanager"
      "video"
      "input"
    ];
    uid = 1000;
    openssh.authorizedKeys.keys = [
      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINtX6CwxVynoCr86hgSrNVmqlzDaTzc9h5z+Sy9n5kYL im@lcnbr.ch"
    ];
  };

  programs.nix-ld.enable = true;

  users.users.vhirschi = {
    isNormalUser = true;
    extraGroups = [
      "wheel"
      "networkmanager"
      "video"
      "input"
    ];
    uid = 1001;
    openssh.authorizedKeys.keys = [
      "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDGH3TKx0kYGIqcAfB2LmYktsJKnZC8lExz4vymEgBR/z7M11pdf/QgPDYXuLQ+f2LqtMNk5cA7kcmKT+j8H93KrzUOYnfItMR1oRVXbDnvLcLxwtIlXV4MBFxZkMocNBMSpuI9sDLaNeDBvIxBIp80DCFENgXzIRbY4FC2ghnFJHXo+k0Gru+JD7kFoFM8yQmszpWqOqS0J64gA/u8Qx6HdJWGpNslaQbYc9jCKCPXXsJXlBNWTB+HZY9ufZXQdvetsueTMUJJIs4aqHfYbYjN7BUGBLm2wmcp2vz5BAROMbHH/c1Zmxa0GWWfrQQkFru/SHiB30OMKRnR0cjIIRmFtuaMjwAlEkzUT6OF+b2baMmRmXFLfKzpTEDun1UysmrbfzRLjbHPW+s4xIH9Nrhn5ggiY4x2Yf5QGOB4DaehAWjqyAKONhNwRB0m0CaTBzd4jTWjsc0m5/iuP2JzGsiFLcWONVH2k/4/Olh/gbQCNz+FCRoBebNwZZ31FwFT6AE= vjhirsch@itp80.unibe.ch"
    ];
  };
  environment.persistence."/persist" = {
    enable = true; # NB: Defaults to true, not needed
    hideMounts = true;
    directories = [
      "/var/log"
      "/var/lib/bluetooth"
      "/var/lib/nixos"
      "/var/lib/systemd/coredump"
      "/etc/NetworkManager/system-connections"
      {
        directory = "/var/lib/colord";
        user = "colord";
        group = "colord";
        mode = "u=rwx,g=rx,o=";
      }
    ];
    users.lcnbr = {
      directories = [
        "documents"
        "media"
        "dev"
        {
          directory = ".gnupg";
          mode = "0700";
        }
        {
          directory = ".ssh";
          mode = "0700";
        }
        {
          directory = ".nixops";
          mode = "0700";
        }
        {
          directory = ".local/share/keyrings";
          mode = "0700";
        }
        ".local/share/direnv"
      ];
      files = [
        ".screenrc"
      ];
    };
    users.vhirschi = {
      directories = [
        "documents"
        "media"
        "dev"
        {
          directory = ".gnupg";
          mode = "0700";
        }
        {
          directory = ".ssh";
          mode = "0700";
        }
        {
          directory = ".nixops";
          mode = "0700";
        }
        {
          directory = ".local/share/keyrings";
          mode = "0700";
        }
        ".local/share/direnv"
      ];
      files = [
        ".screenrc"
      ];
    };
  };

  # You can get your disk id by running the following command on the installer:
  # Replace <IP> with the IP of the installer printed on the screen or by running the `ip addr` command.
  # ssh root@<IP> lsblk --output NAME,ID-LINK,FSTYPE,SIZE,MOUNTPOINT
  disko.devices.disk.main.device = "/dev/disk/by-id/wwn-0x55cd2e41503ed9cb";

  system.stateVersion = "25.05";
}
