# syntax=docker/dockerfile:1
FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
# Optional session CA for cloud builds; never retained in an image layer.
RUN --mount=type=secret,id=proxy_ca \
    if [ -f /run/secrets/proxy_ca ]; then export SSL_CERT_FILE=/run/secrets/proxy_ca; fi; \
    go mod download
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/ordarr ./cmd/ordarr

FROM debian:bookworm-slim
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /out/ordarr /usr/local/bin/ordarr
RUN mkdir /data && chown 10001:10001 /data && chmod 700 /data
USER 10001:10001
ENV ORDARR_ADDR=0.0.0.0:8080 ORDARR_DATA_DIR=/data
EXPOSE 8080
VOLUME /data
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["/usr/local/bin/ordarr", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/ordarr"]
