let
  adminSshKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA3c4BHqDDrZGI6WrbwO5MEg+blmSy7igkQS+miH5roX";
in {
  groups = {
    nfs = {gid = 2000;};
    mercury = {gid = 1000;};
  };

  users = [
    {
      name = "mercury";
      uid = 1000;
      isNormalUser = true;
      shell = "/run/current-system/sw/bin/fish";
      extraGroups = [
        "nfs"
        "mercury"
        "wheel"
        "networkmanager"
        "video"
        "input"
      ];
      sshKeys = [
        adminSshKey
      ];
    }
    {
      name = "lcnbr";
      uid = 1001;
      isNormalUser = true;
      shell = "/run/current-system/sw/bin/fish";
      extraGroups = ["nfs" "wheel"];
      sshKeys = [
        adminSshKey
      ];
    }

    {
      name = "vhirschi";
      isNormalUser = true;
      uid = 1002;
      extraGroups = ["nfs"];
      sshKeys = [
        "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDGH3TKx0kYGIqcAfB2LmYktsJKnZC8lExz4vymEgBR/z7M11pdf/QgPDYXuLQ+f2LqtMNk5cA7kcmKT+j8H93KrzUOYnfItMR1oRVXbDnvLcLxwtIlXV4MBFxZkMocNBMSpuI0DCFENgXzIRbY4FC2ghnFJHXo+k0Gru+JD7kFoFM8yQmszpWqOqS0J64gA/u8Qx6HdJWGpNslaQbYc9jCKCPXXsJXlBNWTB+HZY9ufZXQdvetsueTMUJJIs4aqHfYbYjN7BUGBLm2wmcp2vz5BAROMbHH/c1Zmxa0GWWfrQQkFru/SHiB30OMKRnR0cjIIRmFtuaMjwAlEkzUT6OF+b2baMmRmXFLfKzpTEDun1UysmrbfzRLjbHPW+s4xIH9Nrhn5ggiY4x2Yf5QGOB4DaehAWjqyAKONhNwRB0m0CaTBzd4jTWjsc0m5/iuP2JzGsiFLcWONVH2k/4/Olh/gbQCNz+FCRoBebNwZZ31FwFT6AE= vjhirsch@itp80.unibe.ch"
        adminSshKey
      ];
    }
    {
      name = "zeno";
      isNormalUser = true;
      uid = 1104;
      extraGroups = ["nfs"];
      sshKeys = [
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMNDaHkA5iK8S63BuPRZ5wJecrIp8/wxGP4Hfr/n/vvG zeno.capatti@unibe.ch"
        adminSshKey
      ];
    }
    {
      name = "fraaije";
      isNormalUser = true;
      uid = 1105;
      extraGroups = ["nfs"];
      sshKeys = [
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINYLD5sp6QC5L9JKLE8uTiGmFLb4mGdYqRYMY9sxEumd"
        adminSshKey
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIL/fJkq9G9dxsJK7k3lbb/xo7qA6O9Wav6hCX0UGOfow"
      ];
    }
    {
      name = "simone";
      isNormalUser = true;
      uid = 1106;
      extraGroups = ["nfs"];
      sshKeys = [
        "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCKFEK1Zlar9FwsJBlq20k/HVaw54uj2EsdjGvmTkl69CnfvXKfXNJoBzqxKFVGxv/f5FDV4fCAxDvA3O6Akg4eZ5vvfJTFNbCLWWTgRnNo1jqlDeQ2yys9re6pnqv7xy1PGc6rLJ/e0r7I8sErD5uktJfofSRjCb9Vo/WtLIuRRZtRQgxKRbmbgnTTTMnlPSy7IkfuNT+/phu4ImX0JZPW6yma6NX1XzMCXS96ZgJvPmf+T3xs/9k4OIAH9zJ5b3F28HdJ3ITWkX0yeOqzMbpTGnEamCj3qcVEQAxDzG8BiugMR67ucREpZDZ+xfpBTXNyMjUlzAGf5gWGL0WdcIzcUJv3JI5psZv1p92BPGquy2Ri3QOGQWZv1DTiQ8Au371uLB5nIxuM+CCB1q5qLuKED1SPEzBNwdD0gyFs+IxNXrNXFcXQdOsPEhDCFY5VPUw9R9xs7nmZuXoFujyHTXtg5ldH12np8sWvUJ8T7dljK27tu6OV3DDCylOdGFRDFrE="
        adminSshKey
      ];
    }
    {
      name = "alice";
      isNormalUser = true;
      uid = 1107;
      shell = "/run/current-system/sw/bin/fish";
      extraGroups = ["nfs"];
      sshKeys = [
        adminSshKey
      ];
    }

    {
      name = "alice2";
      isNormalUser = true;
      uid = 1109;
      shell = "/run/current-system/sw/bin/fish";
      extraGroups = ["nfs"];
      sshKeys = [
        adminSshKey
      ];
    }
    {
      name = "bob";
      isNormalUser = true;
      uid = 1108;
      shell = "/run/current-system/sw/bin/fish";
      extraGroups = ["nfs"];
      sshKeys = [
        adminSshKey
      ];
    }
    {
      name = "bobby";
      isNormalUser = true;
      uid = 1110;
      extraGroups = ["nfs"];
      sshKeys = [
      ];
    }
  ];
}
