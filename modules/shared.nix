{
  pkgs,
  clan-core,
  config,
  ...
}: {
  imports = [
  ];

  # Locale service discovery and mDNS
  services.avahi.enable = true;

  users.groups.lcnbr = {};

  # Configure SSH manually (replaces clan-core.clanModules.sshd)
  services.openssh.enable = true;
  services.openssh.settings.PasswordAuthentication = false;
  services.openssh.settings.PermitRootLogin = "no";

  environment.systemPackages = with pkgs; [tailscale btop];

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
}
