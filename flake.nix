{
  description = "GPG bridge for WSL";
  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixpkgs-unstable";
  };
  outputs = {
    self,
    nixpkgs,
    ...
  }: let
    supportedSystems = [
      "x86_64-linux"
      "aarch64-linux"
      # "x86_64-darwin"
      # "aarch64-darwin"
    ];

    forAllSystems = f:
      nixpkgs.lib.genAttrs supportedSystems (
        system:
          f (
            import nixpkgs {
              inherit system;
            }
          )
      );
  in {
    packages = forAllSystems (
      pkgs: let
        goArch = pkgs:
          {
            "x86_64" = "amd64";
            "aarch64" = "arm64";
          }.${
            builtins.head (pkgs.lib.strings.splitString "-" pkgs.stdenv.hostPlatform.system)
          };
        makeGoApp = goPkgs:
          (
            goPkgs.buildGoModule {
              pname = "gpg-bridge";
              version = "0.1.0";
              src = ./.;

              vendorHash = null;
              ldflags = ["-s" "-w"];
              postInstall = ''
                mv "$out/bin/windows_${goArch goPkgs}/gpg-bridge.exe" "$out/bin/gpg-bridge.exe"
                rm -d "$out/bin/windows_${goArch goPkgs}"
              '';
            }
          ).overrideAttrs (
            oldAttrs: {
              env =
                (oldAttrs.env or {})
                // {
                  GOOS = "windows";
                  GOARCH = goArch goPkgs;
                };
            }
          );
      in {
        default = makeGoApp pkgs;
      }
    );

    lib = let
      installTypes = {
        gnupgStandalone = {
          gpgconfCmd = "/mnt/c/Program Files/GnuPG/bin/gpgconf.exe";
        };
        gitForWindows = {
          gpgconfCmd = "/mnt/c/Program Files/Git/bin/bash.exe";
          gpgconfArg1 = "-c";
          gpgconfArg2 =
            /*
            bash
            */
            ''
              VALUE="$(gpgconf "$@")"
              EXIT="$?"
              if [ "$EXIT" -eq 0 ] && { [ "$1" = --list-dirs ] || [ "$1" = -L ]; } && [ -n "$2" ] && [ -n "$VALUE" ]; then
                  cygpath -w "$VALUE"
                  EXIT="$?"
              else
                  printf %s\\n "$VALUE"
              fi
              exit "$EXIT"
            '';
          gpgconfArg3 = "gpgconf";
        };
      };
      genericInstallType = nixpkgs.lib.types.submodule {
        options = {
          gpgconfCmd = nixpkgs.lib.mkOption {type = nixpkgs.lib.types.str;};
          gpgconfArg1 = nixpkgs.lib.mkOption {type = nixpkgs.lib.types.nullOr nixpkgs.lib.types.str;};
          gpgconfArg2 = nixpkgs.lib.mkOption {type = nixpkgs.lib.types.nullOr nixpkgs.lib.types.str;};
          gpgconfArg3 = nixpkgs.lib.mkOption {type = nixpkgs.lib.types.nullOr nixpkgs.lib.types.str;};
          gpgconfArg4 = nixpkgs.lib.mkOption {type = nixpkgs.lib.types.nullOr nixpkgs.lib.types.str;};
          gpgconfArg5 = nixpkgs.lib.mkOption {type = nixpkgs.lib.types.nullOr nixpkgs.lib.types.str;};
          gpgconfArg6 = nixpkgs.lib.mkOption {type = nixpkgs.lib.types.nullOr nixpkgs.lib.types.str;};
          gpgconfArg7 = nixpkgs.lib.mkOption {type = nixpkgs.lib.types.nullOr nixpkgs.lib.types.str;};
          gpgconfArg8 = nixpkgs.lib.mkOption {type = nixpkgs.lib.types.nullOr nixpkgs.lib.types.str;};
          gpgconfArg9 = nixpkgs.lib.mkOption {type = nixpkgs.lib.types.nullOr nixpkgs.lib.types.str;};
        };
      };
      installType =
        nixpkgs.lib.types.coercedTo
        (nixpkgs.lib.types.enum (builtins.attrNames installTypes))
        (s: installTypes.${s})
        genericInstallType;
    in {
      inherit installType;
    };

    overlays.default = final: prev: {
      wslPackages.gpg-bridge = self.packages;
    };

    homeManagerModules.default = {
      config,
      lib,
      pkgs,
      ...
    }: let
      cfg = config.programs.gpg-bridge;
      camelToSnake = str: let
        chars = lib.stringToCharacters str;
        processChars = acc: char: let
          isUpper = char == lib.toUpper char && char != lib.toLower char;
          isFirst = acc == "";
          prefix =
            if isUpper && !isFirst
            then "_"
            else "";
          LevinChar = prefix + lib.toLower char;
        in
          acc + LevinChar;
        result = lib.foldl' processChars "" chars;
      in
        result;
      optionToEnv = name: "GPG_BRIDGE_${lib.strings.toUpper (camelToSnake name)}";
      optionToWslEnv = name: "${optionToEnv name}/w${
        if (lib.strings.hasSuffix "Cmd" name)
        then "p"
        else ""
      }";
      mapInstallTypeAttrsToList = f: installConfig:
        lib.mapAttrsToList f (lib.filterAttrs (_: v: v != null) installConfig);
      gpgbridgeConfigText = installConfig:
        lib.concatStringsSep "\n" (
          (
            mapInstallTypeAttrsToList
            (key: value: "${optionToEnv key}=${lib.escapeShellArg value}")
            installConfig
          )
          ++ [
            "WSLENV=${
              lib.concatStringsSep
              ":"
              (
                mapInstallTypeAttrsToList
                (key: _: optionToWslEnv key)
                installConfig
              )
            }"
          ]
        );
      serviceOverrideTemplate = socketName:
      /*
      systemd
      */
      ''
        [Service]
        EnvironmentFile=%E/gpg-bridge/config.env
        ExecStart=
        ExecStart="${lib.getExe' cfg.package "gpg-bridge.exe"}" "${socketName}"
      '';
    in {
      options.programs.gpg-bridge = {
        enable = lib.mkEnableOption "GPG Bridge";
        package = lib.mkPackageOption pkgs ["wslPackages" "gpg-bridge"] {};
        gpgInstallType = lib.mkOption {
          type = self.lib.installType;
          default = "gnupgStandalone";
        };
      };
      config = lib.mkIf cfg.enable {
        home.packages = [cfg.package];
        xdg.configFile."systemd/user/gpg-agent-browser.socket".source = ./gpg-agent-browser.socket;
        xdg.configFile."systemd/user/gpg-agent-browser@.service".source = ./gpg-agent-browser${"@"}.service;
        xdg.configFile."systemd/user/gpg-agent-browser@.service.d/override.conf".text = serviceOverrideTemplate "agent-browser-socket";
        xdg.configFile."systemd/user/gpg-agent-extra.socket".source = ./gpg-agent-extra.socket;
        xdg.configFile."systemd/user/gpg-agent-extra@.service".source = ./gpg-agent-extra${"@"}.service;
        xdg.configFile."systemd/user/gpg-agent-extra@.service.d/override.conf".text = serviceOverrideTemplate "agent-extra-socket";
        xdg.configFile."systemd/user/gpg-agent-ssh.socket".source = ./gpg-agent-ssh.socket;
        xdg.configFile."systemd/user/gpg-agent-ssh@.service".source = ./gpg-agent-ssh${"@"}.service;
        xdg.configFile."systemd/user/gpg-agent-ssh@.service.d/override.conf".text = serviceOverrideTemplate "agent-ssh-socket";
        xdg.configFile."systemd/user/gpg-agent.socket".source = ./gpg-agent.socket;
        xdg.configFile."systemd/user/gpg-agent@.service".source = ./gpg-agent${"@"}.service;
        xdg.configFile."systemd/user/gpg-agent@.service.d/override.conf".text = serviceOverrideTemplate "agent-socket";
        xdg.configFile."gpg-bridge/config.env".text = gpgbridgeConfigText cfg.gpgInstallType;
        programs.gpg.settings = {
          "no-autostart" = true;
        };
      };
    };
  };
}
