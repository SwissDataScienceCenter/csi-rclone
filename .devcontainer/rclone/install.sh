#!/bin/bash

USERNAME="${_REMOTE_USER}"

set -ex

echo "Downloading rclone sources from ${RCLONE_REPOSITORY}:${RCLONE_REF}"
mkdir -p /tmp/rclone
cd /tmp/rclone
git clone "${RCLONE_REPOSITORY}"
cd rclone
git checkout "${RCLONE_REF}"

echo "Building rclone"
make rclone
cd $HOME
rm -rf /tmp/rclone

# Fix the $GOPATH folder
chown -R "${USERNAME}:golang" /go
chmod -R g+r+w /go
