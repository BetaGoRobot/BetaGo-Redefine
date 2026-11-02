# syntax=docker/dockerfile:1
FROM golang:1.26.0-bookworm@sha256:4f7e5f23bfacf4c2934ba70c132532742b6a53f01a4209e2c2eb7bd06c16f0bc AS build
ENV GOTOOLCHAIN=local CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/lark-agent/ ./cmd/lark-agent/
COPY internal/agent/ ./internal/agent/
COPY internal/application/lark/memes/ ./internal/application/lark/memes/
COPY internal/application/lark/memereference/ ./internal/application/lark/memereference/
COPY internal/infrastructure/memestore/ ./internal/infrastructure/memestore/
COPY internal/infrastructure/memeindex/ ./internal/infrastructure/memeindex/
COPY internal/infrastructure/memeruntime/ ./internal/infrastructure/memeruntime/
COPY internal/infrastructure/mcpclient/ ./internal/infrastructure/mcpclient/
COPY pkg/larkemoji/ ./pkg/larkemoji/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -mod=readonly -trimpath -tags=custom_skip_vips -ldflags='-s -w' -o /out/lark-agent ./cmd/lark-agent

FROM debian:bookworm-slim@sha256:ec31d744f83f4a73447ba045aaab9409997efe5cc706eca4890c1be36cc1606a AS media-base
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg poppler-utils \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 agent \
    && useradd --uid 10001 --gid 10001 --no-create-home --home-dir /app agent \
    && install -d -o 10001 -g 10001 /app

FROM media-base AS runtime
COPY --from=build /out/lark-agent /usr/local/bin/lark-agent
WORKDIR /app
USER 10001:10001
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/lark-agent"]
CMD ["-mode", "agent", "-config", "/etc/agent/agent.toml"]
