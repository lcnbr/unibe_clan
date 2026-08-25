{
  config,
  lib,
  pkgs,
  ...
}: let
  userData = import config.unibe.userListFile;
in {
  imports = [
    # contains your disk format and partitioning configuration.
    ../../modules/user-disko.nix
    ../../modules/shared.nix
    ../../modules/zfs-user-management.nix
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
      extraGroups = (userSpec.extraGroups or []) ++ ["users"];
      home = "/home/${userName}";
      description = userSpec.description or "";
      openssh.authorizedKeys.keys = userSpec.sshKeys or [];
      # Enable autologin for mercury by setting empty password
      hashedPassword =
        if userName == "mercury"
        then ""
        else null;
    });

  users.groups = userData.groups;

  services.openssh.enable = true;
  services.openssh.settings.PasswordAuthentication = false;
  services.openssh.settings.PermitRootLogin = "no";
  security.sudo.enable = true;
  security.sudo.wheelNeedsPassword = false;
  clan.core.networking.targetHost = "lcnbr@130.92.184.230";

  security.sudo.execWheelOnly = true;
  networking.hostName = "itpmario";
  networking.hostId = "fe55c453";
  networking.interfaces.enp7s0.ipv4.addresses = [
    {
      address = "130.92.184.230";
      prefixLength = 24;
    }
  ];

  networking.defaultGateway.interface = "enp7s0";

  networking.defaultGateway.address = "130.92.184.1";
  networking.nameservers = ["130.92.9.52" "130.92.9.53"];

  boot.initrd.systemd.enable = true;
  boot.initrd.systemd.emergencyAccess = true;

  programs.nix-ld.enable = true;
  environment.systemPackages = [
    pkgs.ipmitool
  ];

  # Set mercury as the default user for local login and emergency console
  services.getty.autologinUser = "mercury";

  disko.devices.disk.main.device = "/dev/disk/by-id/wwn-0x55cd2e41503ed9ca";

  system.stateVersion = "25.05";
}
