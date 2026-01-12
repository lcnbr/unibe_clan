{
  config,
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
      shell = userSpec.shell or null;
      extraGroups = (userSpec.extraGroups or []) ++ ["users"];
      home = "/home/${userName}";
      description = userSpec.description or "";
      openssh.authorizedKeys.keys = userSpec.sshKeys or [];
    });

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

  boot.initrd.systemd.emergencyAccess = true;

  programs.nix-ld.enable = true;
  environment.systemPackages = with pkgs; [
    ipmitool
    # NVIDIA utilities
    nvtopPackages.nvidia
    pciutils
    cudatoolkit
  ];

  # Set mercury as the default user for local login and emergency console
  services.getty.autologinUser = "mercury";

  disko.devices.disk.main.device = "/dev/disk/by-id/nvme-WUS5EA1A1ESP5E3_240420800175";

  # Enable NVIDIA GPU support for compute workloads
  nixpkgs.config.allowUnfree = true;
  hardware.graphics.enable = true;

  # Load NVIDIA driver for Xorg and Wayland
  services.xserver.videoDrivers = ["nvidia"];

  # Load NVIDIA driver for compute
  boot.kernelModules = ["nvidia" "nvidia_modeset" "nvidia_uvm" "nvidia_drm"];
  boot.blacklistedKernelModules = ["nouveau"];

  hardware.nvidia = {
    modesetting.enable = true;
    powerManagement.enable = false;
    powerManagement.finegrained = false;
    open = false;
    nvidiaSettings = false;
    package = config.boot.kernelPackages.nvidiaPackages.production;
  };

  system.stateVersion = "25.05";
}
