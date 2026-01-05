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
  in [
    "d /shared 0755 root root -"
    userDirRules
  ];

  users.groups = userData.groups;

  services.openssh.enable = true;
  services.openssh.settings.PasswordAuthentication = false;
  services.openssh.settings.PermitRootLogin = "no";
  security.sudo.enable = true;
  security.sudo.wheelNeedsPassword = false;
  clan.core.networking.targetHost = "mercury@130.92.184.229";

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
  # boot.supportedFilesystems = [ "zfs" ];
  # boot.zfs.devNodes = "/dev/disk/by-id";
  # boot.zfs.forceImportRoot = false;
  boot.initrd.systemd.emergencyAccess = true;

  programs.nix-ld.enable = true;
  environment.systemPackages = [
    pkgs.ipmitool
  ];

  # Set mercury as the default user for local login and emergency console
  services.getty.autologinUser = "mercury";

  disko.devices.disk.main.device = "/dev/disk/by-id/wwn-0x55cd2e41503ed9cb";

  system.stateVersion = "25.05";
}
