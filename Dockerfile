FROM golang:1.22-alpine AS build
WORKDIR /src
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /tz-server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && addgroup -S tz && adduser -S -G tz tz
COPY --from=build /tz-server /usr/local/bin/tz-server
RUN mkdir -p /var/lib/tz && chown tz:tz /var/lib/tz
USER tz
EXPOSE 876
VOLUME ["/var/lib/tz"]
ENTRYPOINT ["/usr/local/bin/tz-server"]
