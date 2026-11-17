FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sboe ./cmd/sboe

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -g 1000 sboe && adduser -D -u 1000 -G sboe sboe \
 && mkdir /data && chown sboe:sboe /data
COPY --from=build /out/sboe /usr/local/bin/sboe
ENV SBOE_DB_PATH=/data/sboe.db SBOE_ADDR=:8080 TZ=Europe/Madrid
USER sboe
WORKDIR /data
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/login || exit 1
ENTRYPOINT ["sboe"]
CMD ["serve"]
