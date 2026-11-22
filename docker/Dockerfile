# ggcode container images - layered per-language variants + enterprise registry support
#
# Tag matrix (each --target produces a distinct tag):
#   ggcode:base   core CLI tools only (git/jq/rg/ssh/build-essential + ggcode)
#   ggcode:python base + python3/pip/venv
#   ggcode:node   base + node22/pnpm/yarn/tsc/tsx
#   ggcode:go     base + official Go toolchain
#   ggcode:java   base + openjdk21 + maven + gradle
#   ggcode:dotnet base + dotnet-sdk-9
#   ggcode:web    base + php + ruby
#   ggcode:full   everything above
#
# Enterprise artifact registries via env (applied at container start):
#   PIP_INDEX_URL, NPM_CONFIG_REGISTRY, GOPROXY, GOPRIVATE,
#   MAVEN_MIRROR_URL, GRADLE_REPO_URL, CARGO_REGISTRIES_CRATE_IO_INDEX,
#   DOTNET_NUGET_SOURCE, GEM_MIRROR_URL, APT_MIRROR (build-time ARG)
# See docker/README.md for the full table.

# ==================== stage 0: builder ====================
FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOFLAGS=-buildvcs=false go build -tags goolm -trimpath \
    -ldflags "-s -w" -o /out/ggcode ./cmd/ggcode

# ==================== stage 1: base (all variants) ====================
FROM ubuntu:24.04 AS base
ARG DEBIAN_FRONTEND=noninteractive
# optional build-time apt mirror, e.g. https://mirrors.internal.com/ubuntu
ARG APT_MIRROR=""
RUN if [ -n "$APT_MIRROR" ]; then \
      sed -i "s|http://archive.ubuntu.com|$APT_MIRROR|g; s|http://security.ubuntu.com|$APT_MIRROR|g" /etc/apt/sources.list.d/ubuntu.sources; \
    fi \
    && apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl wget git jq ripgrep fd-find \
      unzip zip tar xz-utils less vim nano \
      openssh-client gnupg sudo locales tini \
      build-essential gcc g++ make cmake pkg-config \
    && rm -rf /var/lib/apt/lists/*
RUN sed -i 's/# en_US.UTF-8/en_US.UTF-8/' /etc/locale.gen && locale-gen
ENV LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8

# user ggcode uid/gid 1024, passwordless sudo, HOME=WORKDIR=/ggcode
RUN groupadd -g 1024 ggcode \
    && useradd -m -u 1024 -g 1024 -s /bin/bash ggcode \
    && echo "ggcode ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/ggcode \
    && chmod 0440 /etc/sudoers.d/ggcode \
    && mkdir -p /ggcode && chown -R ggcode:ggcode /ggcode

COPY --from=builder /out/ggcode /usr/local/bin/ggcode
COPY docker/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/ggcode /usr/local/bin/entrypoint.sh

USER ggcode
ENV HOME=/ggcode
WORKDIR /ggcode
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/entrypoint.sh"]
CMD ["--plain"]

# ==================== per-language stages ====================
FROM base AS python
USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
      python3 python3-pip python3-venv \
    && rm -rf /var/lib/apt/lists/*
USER ggcode

FROM base AS node
USER root
ARG NODE_MAJOR=22
RUN curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && npm install -g pnpm yarn typescript tsx \
    && rm -rf /var/lib/apt/lists/*
USER ggcode

FROM base AS go
USER root
ARG GO_VERSION=1.26.10
ARG GOARCH=amd64
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz" \
      | tar -C /usr/local -xz && ln -sf /usr/local/go/bin/go /usr/local/bin/go
USER ggcode

FROM base AS java
USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
      openjdk-21-jdk-headless maven gradle \
    && rm -rf /var/lib/apt/lists/*
USER ggcode

FROM base AS dotnet
USER root
RUN apt-get update && apt-get install -y --no-install-recommends dotnet-sdk-9.0 \
    && rm -rf /var/lib/apt/lists/*
USER ggcode

FROM base AS web
USER root
RUN apt-get update && apt-get install -y --no-install-recommends ruby-full php-cli \
    && rm -rf /var/lib/apt/lists/*
USER ggcode

# ==================== full: everything ====================
FROM base AS full
USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
      python3 python3-pip python3-venv \
      openjdk-21-jdk-headless maven gradle \
      ruby-full php-cli dotnet-sdk-9.0 \
    && rm -rf /var/lib/apt/lists/*
ARG NODE_MAJOR=22
RUN curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && npm install -g pnpm yarn typescript tsx \
    && rm -rf /var/lib/apt/lists/*
ARG GO_VERSION=1.26.10
ARG GOARCH=amd64
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz" \
      | tar -C /usr/local -xz && ln -sf /usr/local/go/bin/go /usr/local/bin/go
USER ggcode
