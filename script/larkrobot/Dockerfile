FROM golang:1.25-alpine AS prebuild

ENV GO111MODULE="auto"

WORKDIR /data/

RUN cat /etc/resolv.conf

RUN apk update && \
    apk add -U tzdata && \
    apk add upx && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone

RUN apk add vips-dev g++


FROM prebuild AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd cmd/larkrobot && go build -o /app/larkrobot

RUN --mount=type=cache,target=/go/pkg/mod \
    mkdir -p /tmp/jieba_dict && \
    cp -r /go/pkg/mod/github.com/yanyiwu/gojieba@v1.4.6/deps/cppjieba/dict /tmp/jieba_dict


FROM kevinmatt/libvips AS runner

COPY --from=builder /tmp/jieba_dict/dict /go/pkg/mod/github.com/yanyiwu/gojieba@v1.4.6/deps/cppjieba/dict
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo/ /usr/share/zoneinfo
COPY --from=builder /app/larkrobot /larkrobot
# COPY --from=builder /app/fonts /data/fonts
# COPY --from=builder /app/assets/images /data/images

WORKDIR /

CMD ["./larkrobot"]