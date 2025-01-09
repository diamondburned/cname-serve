{ self, ... }:

{
  config,
  lib,
  pkgs,
  ...
}:

let
  tomlFormat = pkgs.formats.toml { };
  configFile = tomlFormat.generate "cname-serve.toml" config.services.cname-serve.config;

  flags =
    let
      flags = [
        "-c"
        configFile
      ] ++ (lib.lists.optional config.services.cname-serve.verbose "-v");
    in
    lib.escapeShellArgs flags;
in

{
  options.services.cname-serve = {
    enable = lib.mkEnableOption "cname-serve";

    config = lib.mkOption {
      description = "The configuration (in TOML) for cname-serve.";
      type = tomlFormat.type;
    };

    verbose = lib.mkOption {
      description = "Enable debug mode for cname-serve.";
      type = lib.types.bool;
      default = false;
    };

    environmentFile = lib.mkOption {
      description = "The environment file to use for cname-serve.";
      type = lib.types.nullOr lib.types.path;
      default = null;
    };

    package = lib.mkOption {
      description = "The package to use for cname-serve.";
      type = lib.types.package;
      default = (pkgs.extend self.overlays.default).cname-serve;
    };
  };

  config = lib.mkIf config.services.cname-serve.enable {
    systemd.services.cname-serve = {
      description = "cname-serve";
      after = [ "network.target" ];
      wantedBy = [ "multi-user.target" ];
      serviceConfig =
        {
          ExecStart = "${lib.getExe config.services.cname-serve.package} ${flags}";
          DynamicUser = true;
          ConfigurationDirectory = "cname-serve";
          NoNewPrivileges = true;
          AmbientCapabilities = [
            "CAP_NET_BIND_SERVICE"
          ];
          CapabilityBoundingSet = [
            "CAP_NET_BIND_SERVICE"
          ];
        }
        // (lib.optionalAttrs (config.services.cname-serve.environmentFile != null) {
          EnvironmentFile = config.services.cname-serve.environmentFile;
        });
    };
  };
}
