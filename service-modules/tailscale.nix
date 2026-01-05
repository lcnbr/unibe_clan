{
  _class = "clan.service";
  manifest.name = "tailscale";
  manifest.description = "Tailscale mesh VPN";

  roles.peer = {
    description = "Tailscale peer";
    perInstance = { ... }: {
      nixosModule = {config, ...}: {
        services.tailscale.enable = true;
        services.tailscale.authKeyFile =
          config.clan.core.vars.generators.tailscale-auth-key.files.auth-key.path;
        networking.firewall.allowedUDPPorts = [config.services.tailscale.port];
      };
    };
  };
}
