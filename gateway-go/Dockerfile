FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nous-gateway ./cmd/gateway

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates curl fonts-dejavu-core \
        libreoffice-core libreoffice-writer libreoffice-calc libreoffice-impress \
    && rm -rf /var/lib/apt/lists/* \
    && addgroup --system nous \
    && adduser --system --ingroup nous --home /app nous \
    && mkdir -p /data/workspaces /agent/skills/custom /etc/nous-agent /config \
    && chown -R nous:nous /app /data /agent/skills/custom /etc/nous-agent /config

COPY --from=build /out/nous-gateway /usr/local/bin/nous-gateway

USER nous
EXPOSE 7777
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD curl --fail --silent http://127.0.0.1:7777/health >/dev/null || exit 1
ENTRYPOINT ["nous-gateway"]
