
# CSI rclone mount plugin

This project implements Container Storage Interface (CSI) plugin that allows using [rclone mount](https://rclone.org/) as storage backend. Rclone mount points and [parameters](https://rclone.org/commands/rclone_mount/) can be configured using Secret or PersistentVolume volumeAttibutes. 

## Usage

The easiest way to use this driver is to just create a Persistent Volume Claim (PVC) with the `csi-rclone`
storage class. Or if you have modified the storage class name in the `values.yaml` file then use the name you have chosen.
Note that since the storage is backed by an existing cloud storage like S3 or something similar the size 
that is requested in the PVC below has no role at all and is completely ignored. It just has to be provided in the PVC specification.

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: csi-rclone-example
  namespace: csi-rclone-example
  annotations:
    csi-rclone.dev/secretName: csi-rclone-example-secret
spec:
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: 10Gi
  storageClassName: csi-rclone
```

You have to provide a secret with the rclone configuration. The secret has to have a specific format explained below.
The secret can be passed to the CSI driver via the annotation `csi-rclone.dev/secretName`.

The secret requires the following fields:
- `remote`: The name of the remote that should be mounted - has to match the section name in the `configData` field
- `remotePath`: The path on the remote that should be mounted, it should start with the container itself, for example
  for a S3 bucket, if the bucket is called `test_bucket`, then the remote should be at least `test_bucket/`.
- `configData`: The rclone configuration, has to match the JSON schema from `rclone config providers`

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: csi-rclone-example-secret
  namespace: csi-rclone-example
type: Opaque
stringData:
  remote: giab
  remotePath: giab/
  configData: |
    [giab]
    type = s3
    provider = AWS

```

### Skip provisioning and create PV directly

This is more complicated but doable. Here you have to specify the secret name in the CSI parameters.
Assuming that the secret that contains the configuration is called `csi-rclone-example-secret` and 
is located in the namespace `csi-rclone-example-secret-namespace`, then the PV specification would look as follows.

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: csi-rclone-pv-example
spec:
  accessModes:
    - ReadWriteMany
  capacity:
    storage: 10Gi
  csi:
    driver: csi-rclone
    volumeAttributes:
      nodePublishSecretRef: csi-rclone-example-secret
      nodePublisSecretRefNamespace: csi-rclone-example-secret-namespace
  persistentVolumeReclaimPolicy: Delete
```

## Installation

You can install the CSI plugin via Helm. Please checkout the default values file at `deploy/csi-rclone/values.yaml`
in this repository for the possible options on how to configure the installation.

```bash
helm repo add renku https://swissdatasciencecenter.github.io/helm-charts
helm repo update
helm install csi-rclone renku/csi-rclone
```

## Changelog

See [CHANGELOG.txt](CHANGELOG.txt)

## Dev Environment
This repo uses `nix` for the dev environment. Alternatively, run `nix develop` to enter a dev shell.

Ensure that `nix`, `direnv` and `nix-direnv` are installed.
Also add the following to your nix.conf:
```
experimental-features = nix-command flakes
```
then commands can be run like e.g. `nix run '.#initKind'`. Check `flakes.nix` 
for all available commands.

To deploy the test cluster and run tests, run 
```bash
$ nix run '.#initKind'
$ nix run '.#getKubeconfig'
$ nix run '.#deployToKind'
$ go test -v ./...
```
in your shell, or if you're in a nix shell, run
```bash
$ init-kind-cluster
$ get-kind-kubeconfig
$ local-deploy
$ go test -v ./...
```
