ARG RCLONE_IMAGE_REPOSITORY="ghcr.io/swissdatasciencecenter/rclone"
ARG RCLONE_IMAGE_TAG="sha-1f5fcf2"
FROM ${RCLONE_IMAGE_REPOSITORY}:${RCLONE_IMAGE_TAG} AS rclone

FROM golang:1.23.8-bookworm AS build
COPY go.mod go.sum ./
COPY cmd/ ./cmd/
COPY pkg/ ./pkg/
RUN go build -o /csi-rclone cmd/csi-rclone-plugin/main.go

FROM debian:bookworm-slim
# NOTE: the rclone needs ca-certificates and fuse3 to successfully mount cloud storage 
RUN apt-get update && apt-get install -y fuse3 ca-certificates && rm -rf /var/cache/apt/archives /var/lib/apt/lists/*
COPY --from=build /csi-rclone /csi-rclone
COPY --from=rclone --chmod=755 /rclone /usr/bin/
ENTRYPOINT ["/csi-rclone"]
