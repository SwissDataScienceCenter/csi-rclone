FROM golang:1.23.0-bookworm AS build
ARG RCLONE_VERSION=v1.65.2
ARG RCLONE_ARCH=amd64
ARG RCLONE_OS=linux
COPY go.mod go.sum ./
COPY cmd/ ./cmd/
COPY pkg/ ./pkg/
RUN go build -o /csi-rclone cmd/csi-rclone-plugin/main.go
RUN apt-get update && apt-get install -y unzip && \
    curl https://downloads.rclone.org/${RCLONE_VERSION}/rclone-${RCLONE_VERSION}-${RCLONE_OS}-${RCLONE_ARCH}.zip -o rclone.zip && \
	unzip rclone.zip -d /rclone-unzip && \
	chmod a+x /rclone-unzip/*/rclone && \
	mv /rclone-unzip/*/rclone /

FROM debian:bookworm-slim
# NOTE: the rclone needs ca-certificates and fuse3 to successfully mount cloud storage 
RUN apt-get update && apt-get install -y fuse3 ca-certificates && rm -rf /var/cache/apt/archives /var/lib/apt/lists/*
COPY --from=build /csi-rclone /csi-rclone
COPY --from=build /rclone /usr/bin/
ENTRYPOINT ["/csi-rclone"]
