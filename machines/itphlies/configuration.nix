{
  config,
  lib,
  pkgs,
  utils,
  ...
}: let
  userData = import config.unibe.userListFile;
  dummyUserNames = map (index: "codex-dummy-${toString index}") (lib.range 0 3);
  codexUserNames =
    [
      "codex"
      "codex-1"
      "codex-2"
      "codex-3"
    ]
    ++ dummyUserNames;
in {
  imports = [
    # contains your disk format and partitioning configuration.
    ../../modules/user-disko.nix
    ../../modules/shared.nix
    ../../modules/zfs-user-management.nix
  ];

  unibe.userListFile = ../../user-lists/itphlies.nix;

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
      # Anchor accounts are reachable only through an administrator's
      # `sudo -iu`; they have neither a usable password nor SSH keys.
      hashedPassword =
        if builtins.elem userName dummyUserNames
        then "!"
        else null;
    });

  users.groups = userData.groups;

  # These account workers intentionally share one centrally managed Home
  # Manager module. Their usernames and home paths are the only per-user
  # values; the installed package set and Codex build are identical.
  home-manager = {
    useGlobalPkgs = true;
    backupFileExtension = "before-codex-unification";
    users = lib.genAttrs codexUserNames (userName: {
      imports = [../../home-manager/codex/home.nix];
      _module.args.codexUsername = userName;
    });
  };

  # The per-user ZFS datasets are mounted by an imperative oneshot rather than
  # ordinary *.mount units. Do not let Home Manager activate into the parent
  # filesystem if that preparation has not completed successfully.
  systemd.services =
    lib.genAttrs (
      map (userName: "home-manager-${utils.escapeSystemdPath userName}") codexUserNames
    ) (_: {
      after = ["zfs-user-datasets.service"];
      requires = ["zfs-user-datasets.service"];
    });

  services.openssh.enable = true;
  services.openssh.settings.PasswordAuthentication = false;
  services.openssh.settings.PermitRootLogin = "no";
  services.openssh.settings.DenyUsers = lib.mkAfter dummyUserNames;
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
  services.codexUsageDashboard.codexVersionTargets = {
    "/nix/store/al1ya5a2myhlvrwrgsiws3gyr621wa32-codex-0.149.0/bin/codex" = "0.149.0";
    "/nix/store/9cb2ijpwa9hcv8i0qmrxl0pc5731xm9w-codex-0.144.1/bin/codex-raw" = "0.144.1";
    "/nix/store/awb88965qgvy4yszdd6wwc8qiadpfvmb-codex-0.152.1/bin/codex" = "0.152.1";
    "/nix/store/d1rizmwbq8fcbv5p6fqb40dzpn3kv4c7-codex-0.153.4/bin/codex" = "0.153.4";
    "/nix/store/lp8pgfpak48rdgxn3pqgjq51i05kjj7i-codex-0.151.0/bin/.codex-wrapped" = "0.151.0";
    "/nix/store/ndqxkdiqk3krw0xynw43p09q5mr9csk7-codex-0.152.1/bin/codex" = "0.152.1";
    "/nix/store/wv1vgl2264lvq80c2zgxqmqbsm07yj8z-codex-0.153.4/bin/codex" = "0.153.4";
  };
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
