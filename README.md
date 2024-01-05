
# CSI rclone mount plugin

This project implements Container Storage Interface (CSI) plugin that allows using [rclone mount](https://rclone.org/) as storage backend. Rclone mount points and [parameters](https://rclone.org/commands/rclone_mount/) can be configured using Secret or PersistentVolume volumeAttibutes. 


## Installing CSI driver to kubernetes cluster

## Changelog

See [CHANGELOG.txt](CHANGELOG.txt)

## Dev Environment
This repo uses `nix` for the dev environment.

Ensure that `nix`, `direnv` and `nix-direnv` are installed.
Also add the following to your nix.conf:
```
experimental-features = nix-command flakes
```
then commands can be run like e.g. `nix run '.#initKind'`. Check `flakes.nix` 
for all available commands.
