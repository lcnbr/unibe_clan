let
  # =============================================================================
  # User Management Configuration
  # =============================================================================
  # This file defines all users for the unibe_clan NixOS machines.
  #
  # Usage:
  #   - To add a new user: Use mkUser or mkAdminUser functions below
  #   - To add SSH keys: Add them to userSshKeys section and reference by name
  #   - Standard users get: bash shell, nfs group, admin SSH key access
  #   - Admin users additionally get: wheel group (sudo access)
  #
  # Shell Policy:
  #   - Default shell is fish for modern shell experience
  #   - Users can customize fish configuration via Home Manager
  #   - All users get fish by default (consistent experience)
  #
  # GitHub SSH Key Fetching:
  #   - Set githubUsername to automatically fetch SSH keys from GitHub
  #   - Requires building with --impure flag: nix build --impure
  #   - Keys are fetched at evaluation time from https://github.com/username.keys
  #
  # Example:
  #   (mkUser { name = "newuser"; uid = 1200; githubUsername = "gh-username"; })
  #
  # Common SSH keys
  adminSshKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA3c4BHqDDrZGI6WrbwO5MEg+blmSy7igkQS+miH5roX";

  # User SSH keys (extracted for readability)
  userSshKeys = {
    vhirschi = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDGH3TKx0kYGIqcAfB2LmYktsJKnZC8lExz4vymEgBR/z7M11pdf/QgPDYXuLQ+f2LqtMNk5cA7kcmKT+j8H93KrzUOYnfItMR1oRVXbDnvLcLxwtIlXV4MBFxZkMocNBMSpuI9sDLaNeDBvIxBIp80DCFENgXzIRbY4FC2ghnFJHXo+k0Gru+JD7kFoFM8yQmszpWqOqS0J64gA/u8Qx6HdJWGpNslaQbYc9jCKCPXXsJXlBNWTB+HZY9ufZXQdvetsueTMUJJIs4aqHfYbYjN7BUGBLm2wmcp2vz5BAROMbHH/c1Zmxa0GWWfrQQkFru/SHiB30OMKRnR0cjIIRmFtuaMjwAlEkzUT6OF+b2baMmRmXFLfKzpTEDun1UysmrbfzRLjbHPW+s4xIH9Nrhn5ggiY4x2Yf5QGOB4DaehAWjqyAKONhNwRB0m0CaTBzd4jTWjsc0m5/iuP2JzGsiFLcWONVH2k/4/Olh/gbQCNz+FCRoBebNwZZ31FwFT6AE= vjhirsch@itp80.unibe.ch";
    zeno = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMNDaHkA5iK8S63BuPRZ5wJecrIp8/wxGP4Hfr/n/vvG zeno.capatti@unibe.ch";
    fraaije1 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINYLD5sp6QC5L9JKLE8uTiGmFLb4mGdYqRYMY9sxEumd";
    fraaije2 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIL/fJkq9G9dxsJK7k3lbb/xo7qA6O9Wav6hCX0UGOfow";
    simone = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCKFEK1Zlar9FwsJBlq20k/HVaw54uj2EsdjGvmTkl69CnfvXKfXNJoBzqxKFVGxv/f5FDV4fCAxDvA3O6Akg4eZ5vvfJTFNbCLWWTgRnNo1jqlDeQ2yys9re6pnqv7xy1PGc6rLJ/e0r7I8sErD5uktJfofSRjCb9Vo/WtLIuRRZtRQgxKRbmbgnTTTMnlPSy7IkfuNT+/phu4ImX0JZPW6yma6NX1XzMCXS96ZgJvPmf+T3xs/9k4OIAH9zJ5b3F28HdJ3ITWkX0yeOqzMbpTGnEamCj3qcVEQAxDzG8BiugMR67ucREpZDZ+xfpBTXNyMjUlzAGf5gWGL0WdcIzcUJv3JI5psZv1p92BPGquy2Ri3QOGQWZv1DTiQ8Au371uLB5nIxuM+CCB1q5qLuKED1SPEzBNwdD0gyFs+IxNXrNXFcXQdOsPEhDCFY5VPUw9R9xs7nmZuXoFujyHTXtg5ldH12np8sWvUJ8T7dljK27tu6OV3DDCylOdGFRDFrE=";
    ben = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCkxofZueFdq6XXYVssdROo7P9OgEfNNt6fk0uIeELWhNz6/fwIJVTVuz4dEBALh8/gCx5U/M5AOy9laExR9GQJbfxfPUn+w+eibjsBMMdIl0uRWszvfptS+lnvww9y7IFNAjr4qJ9zqxAyDI/jsfFw4WxRafvUXvr82tHAsryDr/xnejxVGAlqYsTqb828qhILobaFAAMropr60vJKZSKwiIv+kTi9Ou787IJl1CPzJ9SA9k1ljjrAFm48yEl0hQ3pUi90a4GVqBfMZcWa2JfGyiPAADc6xxQtIqGBp2mu8iBv5uYfTDo23U/t95Sz8nVlgXk8wXjln2LHBLMwFm8X ben@PC_ONDER";
    nfink = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPLbO4xcldEORXWRP+55UOBnOEaL7mrrk9EA/fDoziIN nic@DESKTOP-AHCILM0";
    kotarela = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ610Tud+GcW6t78+AD8lmA4aoxuKjcjYVDYWvi4jtb0 thkotarelas@gmail.com";
  };

  # Dynamic GitHub SSH key fetching
  # Fetches SSH keys from GitHub's public API
  # Usage: fetchGithubSshKeys "username"
  # Note: Requires --impure flag during evaluation
  fetchGithubSshKeys = username: let
    # Fetch raw key file from GitHub
    keysUrl = "https://github.com/${username}.keys";
    keysFile = builtins.fetchurl keysUrl;
    keysContent = builtins.readFile keysFile;

    # Split content by newlines and filter empty lines
    lines = builtins.split "\n" keysContent;
    # builtins.split returns a list with separators, so we need every other element
    keyLines = builtins.filter (line: builtins.isString line && line != "") lines;
  in
    keyLines;

  # Helper function to create standard users
  mkUser = {
    name,
    uid,
    # Optional overrides
    shell ? "/run/current-system/sw/bin/fish",
    extraGroups ? ["nfs"],
    adminAccess ? false,
    githubUsername ? null,
    personalKeys ? [],
  }: {
    inherit name uid;
    isNormalUser = true;
    inherit shell;
    extraGroups =
      extraGroups
      ++ (
        if adminAccess
        then ["wheel"]
        else []
      );
    sshKeys =
      [adminSshKey]
      ++ personalKeys
      ++ (
        if githubUsername != null
        then fetchGithubSshKeys githubUsername
        else []
      );
  };

  # Helper function for admin users (mercury has special settings)
  mkAdminUser = args: mkUser (args // {adminAccess = true;});

  # Helper to get personal SSH keys
  getPersonalKeys = name:
    if builtins.hasAttr name userSshKeys
    then [userSshKeys.${name}]
    else [];
in {
  groups = {
    nfs = {gid = 2000;};
    mercury = {gid = 1000;};
  };

  users = [
    # System/Admin Users
    {
      name = "mercury";
      uid = 1000;
      isNormalUser = true;
      shell = "/run/current-system/sw/bin/fish";
      extraGroups = ["nfs" "mercury" "wheel" "networkmanager" "video" "input"];
      sshKeys = [adminSshKey];
    }
    (mkAdminUser {
      name = "lcnbr";
      uid = 1001;
    })

    # Regular Users
    (mkUser {
      name = "vhirschi";
      uid = 1002;
      githubUsername = "ValentinHirschi";
      personalKeys = [userSshKeys.vhirschi];
    })
    (mkUser {
      name = "zeno";
      uid = 1104;
      personalKeys = [userSshKeys.zeno];
    })
    (mkUser {
      name = "fraaije";
      uid = 1105;
      personalKeys = [userSshKeys.fraaije1 userSshKeys.fraaije2];
    })
    (mkUser {
      name = "simone";
      uid = 1106;
      personalKeys = [userSshKeys.simone];
    })
    (mkUser {
      name = "alice";
      uid = 1107;
    })
    (mkUser {
      name = "bobby";
      uid = 1108;
    })
    (mkUser {
      name = "ben";
      uid = 1109;
      personalKeys = [userSshKeys.ben];
    })
    (mkUser {
      name = "kaapo";
      uid = 1110;
      githubUsername = "kaapos";
      personalKeys = [];
    })
    (mkUser {
      name = "nfink";
      uid = 1111;
      personalKeys = [userSshKeys.nfink];
    })
    (mkUser {
      name = "cedric";
      uid = 1112;
      githubUsername = "SecretGmG";
      personalKeys = [];
    })
    (mkUser {
      name = "kotarela";
      uid = 1113;
      personalKeys = [userSshKeys.kotarela];
    })
  ];
}
