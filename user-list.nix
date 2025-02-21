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
        # "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINtX6CwxVynoCr86hgSrNVmqlzDaTzc9h5z+Sy9n5kYL im@lcnbr.ch"
      ];
    }
  ];
}
