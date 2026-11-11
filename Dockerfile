FROM golang:1.27.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /out/wishlist-api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -o /out/wishlist-archive ./cmd/archive

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates

FROM runtime AS api
COPY --from=build /out/wishlist-api /usr/local/bin/wishlist-api
USER nobody
CMD ["wishlist-api"]

FROM runtime AS archive
COPY --from=build /out/wishlist-archive /usr/local/bin/wishlist-archive
RUN mkdir /etc/wishlist-cron && \
    printf '0 * * * * /usr/local/bin/wishlist-archive\n' > /etc/wishlist-cron/nobody
CMD ["crond", "-f", "-d", "8", "-c", "/etc/wishlist-cron"]
