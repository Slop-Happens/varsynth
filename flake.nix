{
  description = "Go development environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    { nixpkgs, flake-utils, ... }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
        };
      in
      {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gotools
            gofumpt
            golangci-lint
            delve
            air
          ];

          buildInputs = with pkgs; [
            pkg-config
            openssl
          ];

          shellHook = ''
            export GOPATH="$PWD/.go"
            export GOBIN="$GOPATH/bin"
            export GOCACHE="$PWD/.go/build-cache"
            export PATH="$GOBIN:$PATH"

            mkdir -p "$GOPATH" "$GOBIN" "$GOCACHE"

            echo "Go dev shell ready: $(go version)"
          '';
        };
      }
    );
}
