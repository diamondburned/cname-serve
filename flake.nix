{
  description = "A very basic flake";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }@inputs:

    (flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gotools
          ];
        };

        packages.default = (pkgs.extend self.overlays.default).cname-serve;

        checks.default = import ./nix/vmTest.nix { inherit pkgs self; };
      }
    ))
    // {
      overlays.default = final: prev: {
        cname-serve = prev.callPackage ./nix/package.nix {
          src = self;
        };
      };

      nixosModules.default = import ./nix/module.nix inputs;
    };
}
