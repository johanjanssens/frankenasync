# ──────────────────────────────────────────────
# Global ARGs
# ──────────────────────────────────────────────
ARG PHP_VERSION=8.3
ARG PHP_EXTENSIONS="bcmath,ctype,curl,dom,filter,iconv,mbstring,opcache,openssl,pdo,pdo_sqlite,phar,posix,session,simplexml,sockets,sqlite3,tokenizer,xml,zlib"
ARG PHP_EXTENSION_LIBS="nghttp2"

# ──────────────────────────────────────────────
# Stage 1: Build PHP (ZTS + embed) via static-php-cli
# ──────────────────────────────────────────────
FROM ubuntu:24.04 AS php-builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl ca-certificates \
    make bison re2c flex git autoconf automake autopoint unzip \
    gcc g++ bzip2 cmake patch xz-utils libtool pkg-config && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /php

ARG PHP_VERSION
ARG PHP_EXTENSIONS
ARG PHP_EXTENSION_LIBS

RUN ARCH=$(uname -m) && \
    case "$ARCH" in \
        x86_64)  SPC="spc-linux-x86_64" ;; \
        aarch64) SPC="spc-linux-aarch64" ;; \
    esac && \
    curl -fsSL -o spc "https://dl.static-php.dev/static-php-cli/spc-bin/nightly/${SPC}" && \
    chmod +x spc

RUN --mount=type=secret,id=github_token,env=GITHUB_TOKEN \
    ./spc doctor --auto-fix && \
    ./spc download \
        --with-php=${PHP_VERSION} \
        --for-extensions="${PHP_EXTENSIONS}" \
        --for-libs="${PHP_EXTENSION_LIBS}" \
        --ignore-cache-sources=php-src \
        --prefer-pre-built \
        --retry 5 && \
    ./spc build --enable-zts --build-embed --disable-opcache-jit \
        "${PHP_EXTENSIONS}" \
        --with-libs="${PHP_EXTENSION_LIBS}"

# ──────────────────────────────────────────────
# Stage 2: Build the Go host binary
# ──────────────────────────────────────────────
FROM golang:1.26-alpine AS host-builder

RUN apk add --no-cache git build-base

COPY --from=php-builder /php /php

ARG PHP_EXTENSIONS
ARG PHP_EXTENSION_LIBS

# Clone the FrankenPHP fork
RUN git clone --depth 1 https://github.com/johanjanssens/frankenphp /build/frankenphp

WORKDIR /build/frankenasync
COPY . .

# Point go.mod replace at the cloned fork
RUN go mod edit -replace "$(grep '^replace' go.mod | awk '{print $2 "@" $3}')=/build/frankenphp"

# Resolve CGO flags from the PHP build and compile
RUN --mount=type=cache,target=/go/pkg/mod \
    CGO_CFLAGS="$(cd /php && ./spc spc-config ${PHP_EXTENSIONS} --with-libs=${PHP_EXTENSION_LIBS} --includes)" && \
    CGO_CFLAGS="-fstack-protector-strong -fpic -fpie -O2 -D_LARGEFILE_SOURCE -D_FILE_OFFSET_BITS=64 ${CGO_CFLAGS}" && \
    CGO_LDFLAGS="$(cd /php && ./spc spc-config ${PHP_EXTENSIONS} --with-libs=${PHP_EXTENSION_LIBS} --libs)" && \
    CGO_LDFLAGS="-Wl,-O1 -pie ${CGO_LDFLAGS}" && \
    export CGO_ENABLED=1 CGO_CFLAGS CGO_CPPFLAGS="$CGO_CFLAGS" CGO_LDFLAGS && \
    go build -tags nowatcher -o /frankenasync .

# ──────────────────────────────────────────────
# Stage 3: Runtime
# ──────────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=host-builder /frankenasync ./frankenasync
COPY examples/ ./examples/

ENV FRANKENASYNC_PORT=8081

EXPOSE 8081

CMD ["./frankenasync"]
