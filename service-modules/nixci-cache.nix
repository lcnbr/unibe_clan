{
  _class = "clan.service";
  manifest.name = "nixci-cache";
  manifest.description = "Shared credentials for explicit NixCI cache uploads";
  manifest.readme = builtins.readFile ./nixci-cache.md;

  roles.client = {
    description = "Allow normal users to upload project outputs to NixCI";
    perInstance = {...}: {
      nixosModule = {
        config,
        lib,
        ...
      }: {
        clan.core.vars.generators.nixci-cache = {
          share = true;
          prompts.netrc = {
            description = "Netrc entry for cache.nix-ci.com";
            type = "multiline-hidden";
            persist = true;
          };
          files.netrc = {
            secret = true;
            owner = "root";
            group = "nixci-cache";
            mode = "0440";
          };
        };

        users.groups.nixci-cache.members =
          lib.attrNames (lib.filterAttrs (_: user: user.isNormalUser or false) config.users.users);

        # The project upload command reads this; ordinary Nix builds do not upload.
        environment.sessionVariables.NIXCI_NETRC =
          config.clan.core.vars.generators.nixci-cache.files.netrc.path;
      };
    };
  };
}
