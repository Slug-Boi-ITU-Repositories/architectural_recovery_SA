{
  description = "Python 3.12 + pydriller via venv";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forEachSystem = f:
        builtins.listToAttrs (map (system: { name = system; value = f system; }) systems);
    in {
      devShells = forEachSystem (system:
        let pkgs = nixpkgs.legacyPackages.${system};
        in {
          default = pkgs.mkShell {
            buildInputs = [
              pkgs.python312
              pkgs.python312Packages.pip
              pkgs.python312Packages.virtualenv
            ];

            shellHook = ''
              VENV=".venv"

              # Create venv if needed
              if [ ! -d "$VENV" ]; then
                python -m venv "$VENV"
              fi

              # Activate manually (no sourcing)
              export VIRTUAL_ENV="$(pwd)/$VENV"
              export PATH="$VIRTUAL_ENV/bin:$PATH"

              # Install pydriller only if it's missing
              if ! python -c "import pydriller" 2>/dev/null; then
                echo "Installing pydriller..."
                pip install pydriller
              fi

              echo "Virtual env: $VIRTUAL_ENV"
              echo "Python: $(which python)"
              pip list 2>/dev/null | grep -i pydriller || echo "(pydriller not yet in pip list)"
            '';
          };
        });
    };
}
