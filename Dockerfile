ARG RCLONE_IMAGE_REPOSITORY="ghcr.io/swissdatasciencecenter/rclone"
ARG RCLONE_IMAGE_TAG="sha-308067c"
FROM ${RCLONE_IMAGE_REPOSITORY}:${RCLONE_IMAGE_TAG} AS rclone

FROM golang:1.23.8-bookworm AS build
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
  go mod download
COPY cmd/ ./cmd
COPY pkg/ ./pkg
RUN --mount=type=cache,target=/root/.cache/go-build \
  CGO_ENABLED=1 \
  go build -trimpath -buildvcs=false -ldflags='-s' \
  -o /csi-rclone ./cmd/csi-rclone-plugin

FROM debian:bookworm-slim
RUN <<EOT bash  
  set -ex  
  apt-get update  
  apt-get install -y --no-install-recommends ca-certificates fuse3  
  rm -rf /var/lib/apt/lists/*  
EOT
COPY --from=build /csi-rclone /csi-rclone
COPY --from=rclone --chmod=755 /rclone /usr/bin/

ENTRYPOINT ["/csi-rclone"]