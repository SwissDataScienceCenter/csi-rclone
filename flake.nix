{
  description = "A basic flake with a shell";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  inputs.flake-utils.url = "github:numtide/flake-utils";

  outputs = { self, nixpkgs, flake-utils}:
    flake-utils.lib.eachDefaultSystem (system:
      let
        # Import pkgs (for your platform) and containerPkgs (x86_64-linux/aarch64-linux) for crosscompile on MacOS
        pkgs = nixpkgs.legacyPackages.${system};
        containerPkgsAmd64 = import nixpkgs { localSystem = system; crossSystem = "x86_64-linux"; };
        containerPkgsAArch64 = import nixpkgs { localSystem = system; crossSystem = "aarch64-linux"; };

        goModuleAmd64 = import ./devenv/nix/goModule.nix { inherit pkgs; architecture = "amd64";};
        csiDriverAmd64 = goModuleAmd64.csiDriver;
        csiDriverLinuxAmd64 = goModuleAmd64.csiDriverLinux;

        goModuleAarch64 = import ./devenv/nix/goModule.nix { inherit pkgs; architecture = "arm64";};
        csiDriverAarch64 = goModuleAarch64.csiDriver;
        csiDriverLinuxAarch64 = goModuleAarch64.csiDriverLinux;

        dockerLayerdImageAmd64 = import ./devenv/nix/containerImage.nix {
            inherit pkgs;
            csiDriverLinux = csiDriverLinuxAmd64;
            containerPkgs = containerPkgsAmd64;
            architecture = "amd64";
         };
        dockerLayerdImageAArch64 = import ./devenv/nix/containerImage.nix {
            inherit pkgs;
            csiDriverLinux = csiDriverLinuxAarch64;
            containerPkgs = containerPkgsAArch64;
            architecture = "aarch64";
        };

        scripts = import ./devenv/nix/scripts.nix { inherit pkgs; };
        inherit (scripts) initKindCluster deleteKindCluster getKindKubeconfig localDeployScript reloadScript;

      in
      {
        devShells.default = import ./devenv/nix/shell.nix { inherit pkgs; };

        packages.csi-rclone-binary-amd64 = csiDriverAmd64;
        packages.csi-rclone-binary-linux-amd64 = csiDriverLinuxAmd64;
        packages.csi-rclone-binary-linux-aarch64 = csiDriverLinuxAarch64;
        packages.csi-rclone-container-layerd-amd64 = dockerLayerdImageAmd64;
        packages.csi-rclone-container-layerd-aarch64 = dockerLayerdImageAArch64;
        packages.deployToKind = localDeployScript;
        packages.reload = reloadScript;
        packages.initKind = initKindCluster;
        packages.deleteKind = deleteKindCluster;
        packages.getKubeconfig = getKindKubeconfig;
      });
}
