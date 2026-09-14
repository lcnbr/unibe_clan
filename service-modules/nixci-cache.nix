{
  _class = "clan.service";
  manifest.name = "nixci-cache";
  manifest.description = "Shared NixCI cache downloads and explicit uploads";
  manifest.readme = builtins.readFile ./nixci-cache.md;

  roles.client = {
    description = "Reuse NixCI builds and allow normal users to upload project outputs";
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

        nix.settings = {
          substituters = ["https://cache.nix-ci.com"];
          trusted-public-keys = ["nix-ci:g3xV5BDTLtIBZr/A00IU1x0EtKKlb7YLgBN2SgYgM6A="];
          netrc-file = config.clan.core.vars.generators.nixci-cache.files.netrc.path;
        };

        # The project upload command reads this; ordinary Nix builds do not upload.
        environment.sessionVariables.NIXCI_NETRC =
          config.clan.core.vars.generators.nixci-cache.files.netrc.path;
      };
    };
  };
}
