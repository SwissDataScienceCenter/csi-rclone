FROM golang:1.23.0-bookworm AS build
COPY . .
RUN go build -o /csi-rclone cmd/csi-rclone-plugin/main.go

FROM debian:bookworm-slim
# NOTE: the rclone package in apt does not install ca-certificates or fuse3
# which it both needs to successfully mount cloud storage 
RUN apt-get update && apt-get install -y fuse3 rclone ca-certificates && rm -rf /var/cache/apt/archives /var/lib/apt/lists/*
COPY --from=build /csi-rclone /csi-rclone
ENTRYPOINT ["/csi-rclone"]
