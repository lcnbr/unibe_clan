{lib,pkgs,...}:{
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
  users.users.user.name = "lcnbr";

  # Set this for clan commands use ssh i.e. `clan machines update`
  # If you change the hostname, you need to update this line to root@<new-hostname>
  # This only works however if you have avahi running on your admin machine else use IP
  clan.core.networking.targetHost = "root@130.92.184.147";

  networking.hostName = "itppeach";
  networking.hostId="aaaa3453";

  fileSystems."/persist" =
    {
    neededForBoot=true;
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

   environment.persistence."/persist" = {
       enable = true;  # NB: Defaults to true, not needed
       hideMounts = true;
       directories = [
         "/var/log"
         "/var/lib/bluetooth"
         "/var/lib/nixos"
         "/var/lib/systemd/coredump"
         "/etc/NetworkManager/system-connections"
         { directory = "/var/lib/colord"; user = "colord"; group = "colord"; mode = "u=rwx,g=rx,o="; }
       ];
       users.lcnbr = {
         directories = [
           "documents"
           "media"
           "dev"
           { directory = ".gnupg"; mode = "0700"; }
           { directory = ".ssh"; mode = "0700"; }
           { directory = ".nixops"; mode = "0700"; }
           { directory = ".local/share/keyrings"; mode = "0700"; }
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



  users.users = {
    root={
      openssh.authorizedKeys.keys = [
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINtX6CwxVynoCr86hgSrNVmqlzDaTzc9h5z+Sy9n5kYL im@lcnbr.ch"
      ];
      # initialHashedPassword="$6$1EKwWplF7X6IP7d4$hcpJVomZ4k0LH8lpnNjkgcYJwciDh/fvcOo0/fSrg/z/VT.DQjN4weLg3gtZI4wniETjeycJbQAu6ElTBqFyN0";
    };
    lcnbr = {
      isNormalUser = true;
      # initialHashedPassword="$6$1EKwWplF7X6IP7d4$hcpJVomZ4k0LH8lpnNjkgcYJwciDh/fvcOo0/fSrg/z/VT.DQjN4weLg3gtZI4wniETjeycJbQAu6ElTBqFyN0";
      openssh.authorizedKeys.keys = [
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINtX6CwxVynoCr86hgSrNVmqlzDaTzc9h5z+Sy9n5kYL im@lcnbr.ch"
      ];
      extraGroups = ["wheel" "networkmanager"];
    };
  };


  # Zerotier needs one controller to accept new nodes. Once accepted
  # the controller can be offline and routing still works.
  clan.core.networking.zerotier.controller.enable = true;


  system.stateVersion = "25.05";
}
