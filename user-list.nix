# userlist.nix
{
  groups = {
    nfs = {gid = 2000;};
    mercury = {gid = 1000;};
  };

  users = [
    {
      name = "mercury";
      uid = 1000;
      extraGroups = [
        "nfs"
        "mercury"
        "wheel"
        "networkmanager"
        "video"
        "input"
      ];
      sshKeys = [
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINtX6CwxVynoCr86hgSrNVmqlzDaTzc9h5z+Sy9n5kYL im@lcnbr.ch"
      ];
      # Optional pointer to her Home Manager file
      homeManagerFile = ./users/mercury/home.nix;
    }
    {
      name = "lcnbr";
      uid = 1101;
      isNormalUser = true;
      extraGroups = ["nfs"];
      sshKeys = [
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINtX6CwxVynoCr86hgSrNVmqlzDaTzc9h5z+Sy9n5kYL im@lcnbr.ch"
      ];
      homeManagerFile = ./users/lcnbr/home.nix;
    }
    {
      name = "vhirshi";
      isNormalUser = true;
      uid = 1102;
      extraGroups = ["nfs"];
      sshKeys = [
        "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDGH3TKx0kYGIqcAfB2LmYktsJKnZC8lExz4vymEgBR/z7M11pdf/QgPDYXuLQ+f2LqtMNk5cA7kcmKT+j8H93KrzUOYnfItMR1oRVXbDnvLcLxwtIlXV4MBFxZkMocNBMSpuI9sDLaNeDBvIxBIp80DCFENgXzIRbY4FC2ghnFJHXo+k0Gru+JD7kFoFM8yQmszpWqOqS0J64gA/u8Qx6HdJWGpNslaQbYc9jCKCPXXsJXlBNWTB+HZY9ufZXQdvetsueTMUJJIs4aqHfYbYjN7BUGBLm2wmcp2vz5BAROMbHH/c1Zmxa0GWWfrQQkFru/SHiB30OMKRnR0cjIIRmFtuaMjwAlEkzUT6OF+b2baMmRmXFLfKzpTEDun1UysmrbfzRLjbHPW+s4xIH9Nrhn5ggiY4x2Yf5QGOB4DaehAWjqyAKONhNwRB0m0CaTBzd4jTWjsc0m5/iuP2JzGsiFLcWONVH2k/4/Olh/gbQCNz+FCRoBebNwZZ31FwFT6AE= vjhirsch@itp80.unibe.ch"
      ];
    }

    {
      name = "vhirschi";
      isNormalUser = true;
      uid = 1103;
      extraGroups = ["nfs"];
      sshKeys = [
        "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDGH3TKx0kYGIqcAfB2LmYktsJKnZC8lExz4vymEgBR/z7M11pdf/QgPDYXuLQ+f2LqtMNk5cA7kcmKT+j8H93KrzUOYnfItMR1oRVXbDnvLcLxwtIlXV4MBFxZkMocNBMSpuI9sDLaNeDBvIxBIp80DCFENgXzIRbY4FC2ghnFJHXo+k0Gru+JD7kFoFM8yQmszpWqOqS0J64gA/u8Qx6HdJWGpNslaQbYc9jCKCPXXsJXlBNWTB+HZY9ufZXQdvetsueTMUJJIs4aqHfYbYjN7BUGBLm2wmcp2vz5BAROMbHH/c1Zmxa0GWWfrQQkFru/SHiB30OMKRnR0cjIIRmFtuaMjwAlEkzUT6OF+b2baMmRmXFLfKzpTEDun1UysmrbfzRLjbHPW+s4xIH9Nrhn5ggiY4x2Yf5QGOB4DaehAWjqyAKONhNwRB0m0CaTBzd4jTWjsc0m5/iuP2JzGsiFLcWONVH2k/4/Olh/gbQCNz+FCRoBebNwZZ31FwFT6AE= vjhirsch@itp80.unibe.ch"
      ];
    }
    {
      name = "zeno";
      isNormalUser = true;
      uid = 1104;
      extraGroups = ["nfs"];
      sshKeys = [
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMNDaHkA5iK8S63BuPRZ5wJecrIp8/wxGP4Hfr/n/vvG zeno.capatti@unibe.ch"
      ];
    }
  ];
}
