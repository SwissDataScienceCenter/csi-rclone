{ pkgs}:

let
  csiDriver = pkgs.buildGoModule {
    pname = "csi-rclone-pvc-1";
    version = "0.2.0";
    src = ../../.;
    vendorHash = "sha256-qJL+pmBF1uO9YmZ8aNRI3+phrDLcM4+crqzeKUHr7Nc=";
    # CGO = 0;
    # preBuild = ''
    #   whoami
    #   mkdir -p $TMP/conf
    #   kind get kubeconfig --name csi-rclone-k8s > $TMP/conf/kubeconfig
    #   export KUBECONFIG=$TMP/conf/kubeconfig
    # '';
    # nativeBuildInputs = with pkgs; [ kubectl kind docker ];
    doCheck = false; # tests need docker and kind, which nixbld user might not have access to
  };

  csiDriverLinux = csiDriver.overrideAttrs (old: old // { CGO_ENABLED = 0; GOOS = "linux";  });
in
{
  inherit csiDriver csiDriverLinux ;
}
