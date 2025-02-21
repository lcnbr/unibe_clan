{
  pkgs,
  clan-core,
  config,
  ...
}: {
  imports = [
    # Enables the OpenSSH server for remote access
    clan-core.clanModules.sshd
    # Set a root password
    clan-core.clanModules.root-password
    # clan-core.clanModules.user-password
    clan-core.clanModules.state-version
  ];

  # Locale service discovery and mDNS
  services.avahi.enable = true;

  services.tailscale.enable = true;
  users.groups.lcnbr = {};
  networking.firewall.allowedUDPPorts = [config.services.tailscale.port];

  environment.systemPackages = with pkgs; [tailscale btop];

  # generate a random password for our user below
  # can be read using `clan secrets get <machine-name>-user-password` command
  # clan.user-password.user = "lcnbr";
  users.users = {
    root = {
      openssh.authorizedKeys.keys = [
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINtX6CwxVynoCr86hgSrNVmqlzDaTzc9h5z+Sy9n5kYL im@lcnbr.ch"
      ];
      # initialHashedPassword="$6$1EKwWplF7X6IP7d4$hcpJVomZ4k0LH8lpnNjkgcYJwciDh/fvcOo0/fSrg/z/VT.DQjN4weLg3gtZI4wniETjeycJbQAu6ElTBqFyN0";
    };
    # lcnbr = {
    #   # isNormalUser = true;
    #   # initialHashedPassword="$6$1EKwWplF7X6IP7d4$hcpJVomZ4k0LH8lpnNjkgcYJwciDh/fvcOo0/fSrg/z/VT.DQjN4weLg3gtZI4wniETjeycJbQAu6ElTBqFyN0";
    #   openssh.authorizedKeys.keys = [
    #     "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINtX6CwxVynoCr86hgSrNVmqlzDaTzc9h5z+Sy9n5kYL im@lcnbr.ch"
    #   ];
    #   # extraGroups = ["wheel" "networkmanager"];
    # };
  };


}
