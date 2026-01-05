{pkgs, ...}: {
  home.packages = with pkgs; [
    viddy
    devenv
  ];
  programs = {
    gh = {
      enable = true;
      gitCredentialHelper = {
        enable = true;
      };
    };

    starship = {
      enable = true;
      enableFishIntegration = true;
      enableNushellIntegration = true;
    };

    helix = {
      enable = true;
    };

    bash = {
      enable = true;
    };

    fish = {
      enable = true;
    };

    direnv = {
      enable = true;
      enableBashIntegration = true;
      nix-direnv.enable = true;
    };

    jujutsu = {
      enable = true;
      settings = {
        user = {
          email = "im@lcnbr.ch";
          name = "Lucien Huber";
        };
        revset-aliases = {
          "immutable_heads()" = "builtin_immutable_heads() | remote_bookmarks()";
        };
      };
    };

    git = {
      enable = true;
      userName = "lcnbr";
      userEmail = "im@lcnbr.ch";
      signing = {
        format = "ssh";
      };
    };

    nushell = {
      enable = true;

      extraConfig = ''
        # Define ENV_CONVERSIONS for PATH
        $env.ENV_CONVERSIONS = ($env.ENV_CONVERSIONS? | default {} | merge {
            "PATH": {
                from_string: { |s| $s | split row (char esep) },
                to_string: { |v| $v | str join (char esep) }
            }
        })

        # Pre-prompt hook to integrate direnv
        $env.config = ($env.config? | default {})
        $env.config.hooks = ($env.config.hooks? | default {})
        $env.config.hooks.pre_prompt = (
            $env.config.hooks.pre_prompt? | default [] | append {||
                if (which direnv | is-empty) {
                    return
                }

                # Load environment variables from direnv
                direnv export json | from json --strict | default {} | load-env

                # Ensure PATH is converted using ENV_CONVERSIONS
                if 'ENV_CONVERSIONS' in $env and 'PATH' in $env.ENV_CONVERSIONS {
                    $env.PATH = do $env.ENV_CONVERSIONS.PATH.from_string $env.PATH
                }
            }
        )
      '';
    };

    zellij = {
      enable = true;
      settings = {
        theme = "catppuccin-frappe";
      };
    };
  };

  home.stateVersion = "25.05";
}
