{config, pkgs, ...}: {

  clan.core.vars.generators.root-password = {
    # prompt the user for a password
    # (`password-input` being an arbitrary name)
    prompts.password-input.description = "the root user's password";
    prompts.password-input.type = "hidden";
    # don't store the prompted password itself
    prompts.password-input.persist = false;
    # define an output file for storing the hash
    files.password-hash.secret = false;
    # define the logic for generating the hash
    script = ''
      cat $prompts/password-input | mkpasswd -m sha-512 > $out/password-hash
    '';
    # the tools required by the script
    runtimeInputs = [ pkgs.mkpasswd ];
  };

  # ensure users are immutable (otherwise the following config might be ignored)
  users.mutableUsers = false;
  # set the root password to the file containing the hash
  users.users.root.hashedPasswordFile =
    # clan will make sure, this path exists
    config.clan.core.vars.generators.root-password.files.password-hash.path;
}
