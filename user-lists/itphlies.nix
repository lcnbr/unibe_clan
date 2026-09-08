let
  shared = import ./itppeach-itphlies.nix;

  dummyUsers = [
    {
      name = "codex-dummy-0";
      uid = 1127;
      isNormalUser = true;
      shell = "/run/current-system/sw/bin/fish";
      extraGroups = [ ];
      sshKeys = [ ];
    }
    {
      name = "codex-dummy-1";
      uid = 1128;
      isNormalUser = true;
      shell = "/run/current-system/sw/bin/fish";
      extraGroups = [ ];
      sshKeys = [ ];
    }
    {
      name = "codex-dummy-2";
      uid = 1129;
      isNormalUser = true;
      shell = "/run/current-system/sw/bin/fish";
      extraGroups = [ ];
      sshKeys = [ ];
    }
    {
      name = "codex-dummy-3";
      uid = 1130;
      isNormalUser = true;
      shell = "/run/current-system/sw/bin/fish";
      extraGroups = [ ];
      sshKeys = [ ];
    }
    {
      name = "codex-dummy-4";
      uid = 1131;
      isNormalUser = true;
      shell = "/run/current-system/sw/bin/fish";
      extraGroups = [ ];
      sshKeys = [ ];
    }
    {
      name = "codex-dummy-5";
      uid = 1132;
      isNormalUser = true;
      shell = "/run/current-system/sw/bin/fish";
      extraGroups = [ ];
      sshKeys = [ ];
    }
  ];
in
shared
// {
  # These anchor-only users exist on itphlies, not on itppeach. Their homes
  # are covered by the host's normal per-user ZFS dataset management.
  users = shared.users ++ dummyUsers;
}
