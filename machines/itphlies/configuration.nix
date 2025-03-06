{
  lib,
  pkgs,
  ...
}: let
  userData = import ../../user-list.nix; # The file above
in {
  imports = [
    # contains your disk format and partitioning configuration.
    ../../modules/disko.nix
    ../../modules/shared.nix
    # ../../root-passwd.nix
  ];

  users.users =
    lib.genAttrs
      (map (u: u.name) userData.users)
      (userName: let
        userSpec = lib.findFirst (u: u.name == userName) null userData.users;
      in {
        isNormalUser = true;
        uid = userSpec.uid;
        extraGroups = (userSpec.extraGroups or []) ++ [ "users" "wheel" ];
        home = "/home/${userName}";
        description = userSpec.description or "";
        openssh.authorizedKeys.keys = userSpec.sshKeys or [];
      });

      # security.acls.enable = true;

        # Ensure the directory structure persists
        systemd.tmpfiles.rules = let
          userDirRules = builtins.concatStringsSep "\n" (map (user: ''
            d /shared/${user.name} 0750 ${user.name} users -
          '') userData.users);
        in [
          "d /shared 0755 root root -"
          userDirRules
        ];

  users.groups = userData.groups;

  services.openssh.enable = true;
  services.openssh.passwordAuthentication = false;
  services.openssh.permitRootLogin = "no";

  clan.core.networking.targetHost = "root@itphlies";

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

  disko.devices.disk.main.device = "/dev/disk/by-id/nvme-WUS5EA1A1ESP5E3_240420800175";

  system.stateVersion = "25.05";
}
