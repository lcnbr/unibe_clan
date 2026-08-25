{lib, ...}: {
  options.unibe.userListFile = lib.mkOption {
    type = lib.types.path;
    default = ../user-list.nix;
    description = "Nix file containing the user and group definitions for this machine.";
  };
}
