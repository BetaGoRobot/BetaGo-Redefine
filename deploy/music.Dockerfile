FROM golang:1.26.8-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -tags=custom_skip_vips -trimpath -o /music-server ./cmd/music-server
FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -u 3000 music
COPY --from=build /music-server /usr/local/bin/music-server
USER music
EXPOSE 37831
ENTRYPOINT ["/usr/local/bin/music-server"]
