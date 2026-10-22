# Test image for OwnCI container-runner mode: Ubuntu 22.04 with the runner's
# dependencies (as bin/installdependencies.sh installs them), git, sudo and the
# docker CLI. The runner itself is not in the image: OwnCI mounts it.
FROM ubuntu:22.04
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      libicu70 libkrb5-3 zlib1g libssl3 liblttng-ust1 \
      ca-certificates curl git sudo \
 && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://download.docker.com/linux/static/stable/x86_64/docker-27.5.1.tgz \
    | tar -xz -C /usr/local/bin --strip-components=1 docker/docker \
 && docker --version
