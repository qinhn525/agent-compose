ARG REGISTRY_MIRROR=docker.io
ARG GOPROXY=https://goproxy.cn,direct

FROM ${REGISTRY_MIRROR}/library/golang:1-alpine AS golang-toolchain

FROM ${REGISTRY_MIRROR}/library/debian:bookworm AS boxlite-build
ARG BOXLITE_VERSION=v0.9.5
ARG TARGETARCH
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG ALL_PROXY
ARG NO_PROXY
ENV HTTP_PROXY=${HTTP_PROXY}
ENV HTTPS_PROXY=${HTTPS_PROXY}
ENV ALL_PROXY=${ALL_PROXY}
ENV NO_PROXY=${NO_PROXY}
ENV no_proxy=${NO_PROXY}
RUN if [ -f /etc/apt/sources.list ]; then       sed -i -e 's|deb.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list &&       sed -i -e 's|security.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list;     fi &&     if [ -f /etc/apt/sources.list.d/debian.sources ]; then       sed -i -e 's|deb.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list.d/debian.sources &&       sed -i -e 's|security.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list.d/debian.sources;     fi
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl python3 tar &&     rm -rf /var/lib/apt/lists/*
RUN set -e;     target_arch="${TARGETARCH:-$(dpkg --print-architecture)}";     case "${target_arch}" in       amd64) BOXLITE_ARCH=x64 ;;       arm64) BOXLITE_ARCH=arm64 ;;       *) echo "unsupported BoxLite target arch: ${target_arch}" >&2; exit 1 ;;     esac;     mkdir -p /tmp/boxlite/runtime /tmp/boxlite/sdk /out/include /out/lib /out/runtime &&     BOXLITE_RUNTIME_NAME=boxlite-runtime-${BOXLITE_VERSION}-linux-${BOXLITE_ARCH}-gnu.tar.gz &&     BOXLITE_C_NAME=boxlite-c-${BOXLITE_VERSION}-linux-${BOXLITE_ARCH}-gnu.tar.gz &&     curl --http1.1 --retry 5 --retry-all-errors --retry-delay 2 -fsSL -o /tmp/boxlite/${BOXLITE_RUNTIME_NAME} https://github.com/boxlite-ai/boxlite/releases/download/${BOXLITE_VERSION}/${BOXLITE_RUNTIME_NAME} &&     curl --http1.1 --retry 5 --retry-all-errors --retry-delay 2 -fsSL -o /tmp/boxlite/${BOXLITE_C_NAME} https://github.com/boxlite-ai/boxlite/releases/download/${BOXLITE_VERSION}/${BOXLITE_C_NAME} &&     tar -xzf /tmp/boxlite/${BOXLITE_RUNTIME_NAME} -C /tmp/boxlite/runtime &&     tar -xzf /tmp/boxlite/${BOXLITE_C_NAME} -C /tmp/boxlite/sdk &&     cp -a /tmp/boxlite/runtime/boxlite-runtime/. /out/runtime/ &&     cp /tmp/boxlite/sdk/*/include/boxlite.h /out/include/boxlite.h &&     cp -a /tmp/boxlite/sdk/*/lib/libboxlite.* /out/lib/

FROM ${REGISTRY_MIRROR}/library/rust:1.91-bookworm AS microsandbox-build
ARG MICROSANDBOX_VERSION=v0.5.4
ARG MICROSANDBOX_SDK_REF=824a04f057000c3e2907551537c9574f9c0e09fc
ARG TARGETARCH
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG ALL_PROXY
ARG NO_PROXY
ENV HTTP_PROXY=${HTTP_PROXY}
ENV HTTPS_PROXY=${HTTPS_PROXY}
ENV ALL_PROXY=${ALL_PROXY}
ENV NO_PROXY=${NO_PROXY}
ENV no_proxy=${NO_PROXY}
RUN if [ -f /etc/apt/sources.list ]; then       sed -i -e 's|deb.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list &&       sed -i -e 's|security.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list;     fi &&     if [ -f /etc/apt/sources.list.d/debian.sources ]; then       sed -i -e 's|deb.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list.d/debian.sources &&       sed -i -e 's|security.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list.d/debian.sources;     fi
RUN apt-get update && apt-get install -y --no-install-recommends       build-essential clang curl libcap-ng-dev       libclang-dev pkg-config protobuf-compiler python3 python3-pyelftools tar &&     rm -rf /var/lib/apt/lists/*
ENV PATH=/usr/local/cargo/bin:${PATH}
WORKDIR /src
RUN curl --http1.1 --retry 5 --retry-all-errors --retry-delay 2 -fsSL -o /tmp/microsandbox-src.tar.gz https://github.com/superradcompany/microsandbox/archive/${MICROSANDBOX_SDK_REF}.tar.gz &&     tar -xzf /tmp/microsandbox-src.tar.gz --strip-components=1 -C /src
RUN set -e;     target_arch="${TARGETARCH:-$(dpkg --print-architecture)}";     case "${target_arch}" in       amd64) MICROSANDBOX_ARCH=x86_64 ;;       arm64) MICROSANDBOX_ARCH=aarch64 ;;       *) echo "unsupported Microsandbox target arch: ${target_arch}" >&2; exit 1 ;;     esac;     MICROSANDBOX_RELEASE_BASE=https://github.com/superradcompany/microsandbox/releases/download/${MICROSANDBOX_VERSION} &&     MICROSANDBOX_BUNDLE_NAME=microsandbox-linux-${MICROSANDBOX_ARCH}.tar.gz &&     MICROSANDBOX_AGENTD_NAME=agentd-${MICROSANDBOX_ARCH} &&     mkdir -p /tmp/microsandbox /tmp/microsandbox/extract /tmp/msb-home/bin /tmp/msb-home/lib /src/build /out/bin /out/lib &&     curl --http1.1 --retry 5 --retry-all-errors --retry-delay 2 -fsSL -o /tmp/microsandbox/${MICROSANDBOX_BUNDLE_NAME} ${MICROSANDBOX_RELEASE_BASE}/${MICROSANDBOX_BUNDLE_NAME} &&     curl --http1.1 --retry 5 --retry-all-errors --retry-delay 2 -fsSL -o /tmp/microsandbox/${MICROSANDBOX_AGENTD_NAME} ${MICROSANDBOX_RELEASE_BASE}/${MICROSANDBOX_AGENTD_NAME} &&     curl --http1.1 --retry 5 --retry-all-errors --retry-delay 2 -fsSL -o /tmp/microsandbox/checksums.sha256 ${MICROSANDBOX_RELEASE_BASE}/checksums.sha256 &&     cd /tmp/microsandbox &&     sha256sum -c --ignore-missing checksums.sha256 &&     tar -xzf ${MICROSANDBOX_BUNDLE_NAME} -C /tmp/microsandbox/extract &&     install -m755 /tmp/microsandbox/extract/msb /tmp/msb-home/bin/msb &&     install -m755 /tmp/microsandbox/extract/msb /src/build/msb &&     install -m644 /tmp/microsandbox/extract/libkrunfw.so.5.2.1 /tmp/msb-home/lib/libkrunfw.so.5.2.1 &&     install -m644 /tmp/microsandbox/extract/libkrunfw.so.5.2.1 /src/build/libkrunfw.so.5.2.1 &&     install -m755 ${MICROSANDBOX_AGENTD_NAME} /src/build/agentd &&     ln -sf libkrunfw.so.5.2.1 /tmp/msb-home/lib/libkrunfw.so.5 &&     ln -sf libkrunfw.so.5 /tmp/msb-home/lib/libkrunfw.so &&     cd /src && MSB_HOME=/tmp/msb-home CI=1 cargo build --release -p microsandbox-go &&     install -m755 /tmp/msb-home/bin/msb /out/bin/msb &&     install -m755 /src/build/agentd /out/bin/agentd &&     install -m644 /tmp/msb-home/lib/libkrunfw.so.5.2.1 /out/lib/libkrunfw.so.5.2.1 &&     ln -sf libkrunfw.so.5.2.1 /out/lib/libkrunfw.so.5 &&     ln -sf libkrunfw.so.5 /out/lib/libkrunfw.so &&     install -m644 target/release/libmicrosandbox_go_ffi.so /out/lib/libmicrosandbox_go_ffi.so &&     strip --strip-unneeded /out/lib/libmicrosandbox_go_ffi.so 2>/dev/null

FROM ${REGISTRY_MIRROR}/library/debian:bookworm AS go-build
ARG VERSION=0
ARG TARGETARCH
ARG GOPROXY
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG ALL_PROXY
ARG NO_PROXY
ENV HTTP_PROXY=${HTTP_PROXY}
ENV HTTPS_PROXY=${HTTPS_PROXY}
ENV ALL_PROXY=${ALL_PROXY}
ENV NO_PROXY=${NO_PROXY}
ENV no_proxy=${NO_PROXY}
RUN if [ -f /etc/apt/sources.list ]; then       sed -i -e 's|deb.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list &&       sed -i -e 's|security.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list;     fi &&     if [ -f /etc/apt/sources.list.d/debian.sources ]; then       sed -i -e 's|deb.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list.d/debian.sources &&       sed -i -e 's|security.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list.d/debian.sources;     fi
RUN apt-get update && apt-get install -y --no-install-recommends build-essential ca-certificates curl git tar && rm -rf /var/lib/apt/lists/*
COPY --from=golang-toolchain /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:${PATH}
WORKDIR /app
COPY --from=boxlite-build /out /app/build/boxlite
COPY go.mod go.sum ./
RUN go env -w GOPROXY="${GOPROXY}" && go mod download
COPY cmd ./cmd
COPY pkg ./pkg
COPY assets ./assets
COPY proto ./proto
RUN target_arch="${TARGETARCH:-$(dpkg --print-architecture)}" && CGO_ENABLED=1 GOOS=linux GOARCH=${target_arch} go build -ldflags "-X agent-compose/pkg/config.BuildVersion=${VERSION}" -tags 'netgo,osusergo,boxlitecgo' -o /out/agent-compose ./cmd/agent-compose

FROM scratch AS agent-compose-artifact
COPY --from=go-build /out/agent-compose /out/agent-compose

FROM ${REGISTRY_MIRROR}/library/debian:trixie-slim
RUN if [ -f /etc/apt/sources.list ]; then       sed -i -e 's|deb.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list &&       sed -i -e 's|security.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list;     fi &&     if [ -f /etc/apt/sources.list.d/debian.sources ]; then       sed -i -e 's|deb.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list.d/debian.sources &&       sed -i -e 's|security.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list.d/debian.sources;     fi
RUN apt-get update &&     apt-get install -y --no-install-recommends ca-certificates git python3 tini tzdata e2fsprogs &&     rm -rf /var/lib/apt/lists/*
RUN ln -sfv /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && echo "Asia/Shanghai" > /etc/timezone
WORKDIR /app
COPY --from=go-build /out/agent-compose /app/agent-compose
COPY --from=boxlite-build /out/runtime /app/boxlite/runtime
COPY --from=microsandbox-build /out /app/microsandbox
ENV RUNTIME_DRIVER=docker
ENV DEFAULT_IMAGE=debian:bookworm-slim
ENV GUEST_WORKSPACE=/workspace
ENV GUEST_STATE_ROOT=/data/state
ENV GUEST_RUNTIME_ROOT=/data/runtime
ENV GUEST_LOG_ROOT=/data/logs
ENV BOXLITE_RUNTIME_DIR=/app/boxlite/runtime
ENV MICROSANDBOX_MSB_PATH=/app/microsandbox/bin/msb
ENV MICROSANDBOX_LIB_PATH=/app/microsandbox/lib/libmicrosandbox_go_ffi.so
ENV BOXLITE_RUNTIME_DIR=/app/boxlite/runtime
ENV LD_LIBRARY_PATH=/app/boxlite/runtime:/app/microsandbox/lib
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/app/agent-compose", "daemon"]
