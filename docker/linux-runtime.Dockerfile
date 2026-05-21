FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        libarchive-tools \
        mount \
        squashfs-tools \
        util-linux \
        xz-utils \
        zstd \
        time \
    && rm -rf /var/lib/apt/lists/*

COPY lfl /usr/local/bin/lfl
ENTRYPOINT ["/usr/local/bin/lfl"]
