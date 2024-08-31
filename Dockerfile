FROM golang:1.23.0-bookworm AS build
COPY . .
RUN go build -o /csi-rclone cmd/csi-rclone-plugin/main.go
RUN apt-get update && apt-get install -y unzip && \
    curl -O https://downloads.rclone.org/rclone-current-linux-amd64.zip && \
    unzip rclone-current-linux-amd64.zip && \
    cp rclone-*-linux-amd64/rclone /rclone

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y fuse3 && rm -rf /var/cache/apt/archives /var/lib/apt/lists/*
COPY --from=build /csi-rclone /csi-rclone
COPY --from=build /rclone /usr/bin/rclone
ENTRYPOINT ["/csi-rclone"]
