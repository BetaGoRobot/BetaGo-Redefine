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

RUN go mod download

COPY . .

RUN cd cmd/larkrobot && \
    go build -o /app/larkrobot


FROM kevinmatt/libvips AS runner

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo/ /usr/share/zoneinfo
COPY --from=builder /app/larkrobot /larkrobot
# COPY --from=builder /app/fonts /data/fonts
# COPY --from=builder /app/assets/images /data/images

WORKDIR /

CMD ["./larkrobot"]