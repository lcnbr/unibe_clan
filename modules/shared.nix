{
  pkgs,
  clan-core,
  config,
  lib,
  ...
}: let
  userData = import ../user-list.nix;
  defaultHomeConfig = ../users/default/home.nix;
in {
  imports = [
  ];

  # Configure Nix for flake-based systems
  nix = {
    # Set nixPath to flake's nixpkgs for compatibility while avoiding channel warnings
    nixPath = ["nixpkgs=${pkgs.path}"];

    # Enable flakes and new nix command
    settings = {
      experimental-features = ["nix-command" "flakes"];
      # Prevent looking for channels
      auto-optimise-store = true;
      # Increase download buffer size to prevent warnings
      download-buffer-size = 134217728; # 128 MiB
    };

    # Clean up old generations automatically
    gc = {
      automatic = true;
      dates = "weekly";
      options = "--delete-older-than 30d";
    };
  };

  # Locale service discovery and mDNS
  services.avahi.enable = true;

  users.groups.lcnbr = {};

  # Configure SSH manually (replaces clan-core.clanModules.sshd)
  services.openssh.enable = true;
  services.openssh.settings.PasswordAuthentication = false;
  services.openssh.settings.PermitRootLogin = "no";
  services.openssh.settings.DenyUsers = ["mercury"];

  environment.systemPackages = with pkgs; [
    tailscale
    btop
    # Home Manager for self-managed users
    home-manager
    # Script to help users set up standalone Home Manager
    (pkgs.writeShellScriptBin "setup-home-manager" (builtins.readFile ../scripts/setup-home-manager.sh))
  ];

  # Make Home Manager templates available to users
  environment.etc."home-manager-templates/default-standalone-home.nix".source = ../home-manager-templates/default-standalone-home.nix;
  environment.etc."home-manager-templates/advanced-home.nix".source = ../home-manager-templates/advanced-home.nix;
  environment.etc."home-manager-templates/README.md".source = ../home-manager-templates/README.md;

  # Enable fish shell system-wide
  programs.fish.enable = true;

  clan.core.vars.generators.tailscale-auth-key = {
    share = true;
    prompts.auth-key.description = "tailscale auth key";
    prompts.auth-key.type = "hidden";
    prompts.auth-key.persist = false;
    files.auth-key.secret = true;
    script = ''
      cat $prompts/auth-key > $out/auth-key
    '';
  };

  # generate a random password for our user below
  # can be read using `clan secrets get <machine-name>-user-password` command
  # clan.user-password.user = "lcnbr";
  users.users = {
    root = {
      openssh.authorizedKeys.keys = [
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA3c4BHqDDrZGI6WrbwO5MEg+blmSy7igkQS+miH5roX"
      ];
      # initialHashedPassword="$6$1EKwWplF7X6IP7d4$hcpJVomZ4k0LH8lpnNjkgcYJwciDh/fvcOo0/fSrg/z/VT.DQjN4weLg3gtZI4wniETjeycJbQAu6ElTBqFyN0";
    };
    # lcnbr = {
    #   # isNormalUser = true;
    #   # initialHashedPassword="$6$1EKwWplF7X6IP7d4$hcpJVomZ4k0LH8lpnNjkgcYJwciDh/fvcOo0/fSrg/z/VT.DQjN4weLg3gtZI4wniETjeycJbQAu6ElTBqFyN0";
    #   openssh.authorizedKeys.keys = [
    #     "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA3c4BHqDDrZGI6WrbwO5MEg+blmSy7igkQS+miH5roX"
    #   ];
    #   # extraGroups = ["wheel" "networkmanager"];
    # };
  };

  # Home Manager configuration - only for users who don't manage their own
  home-manager = {
    useGlobalPkgs = true;
    useUserPackages = true;
    backupFileExtension = "backup";
    users = let
      # Only configure users who don't have standalone home-manager
      managedUsers = lib.filter (u: !(u ? standaloneHomeManager && u.standaloneHomeManager)) userData.users;
      homeManagerUsers =
        lib.genAttrs
        (map (u: u.name) managedUsers)
        (userName: let
          userSpec = lib.findFirst (u: u.name == userName) null userData.users;
          homeConfig =
            if userSpec ? homeManagerFile && userSpec.homeManagerFile != null
            then userSpec.homeManagerFile
            else defaultHomeConfig;
        in
          import homeConfig);
    in
      homeManagerUsers;
  };
}
