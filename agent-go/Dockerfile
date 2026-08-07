# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build

RUN apk add --no-cache ca-certificates git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -tags=docker -ldflags="-s -w" -o /out/agentd ./cmd/agentd \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agentctl ./cmd/agentctl

FROM alpine:3.22

RUN apk add --no-cache bash ca-certificates curl git tzdata \
    && addgroup -S nous \
    && adduser -S -G nous -h /app nous \
    && mkdir -p /app/.nous-agent/workspaces /agent/skills/public /etc/nous-agent \
    && chown -R nous:nous /app /agent/skills

WORKDIR /app
COPY --from=build /out/agentd /usr/local/bin/agentd
COPY --from=build /out/agentctl /usr/local/bin/agentctl
COPY --chown=nous:nous config.example.yaml /etc/nous-agent/config.yaml

USER nous

EXPOSE 7776
VOLUME ["/app/.nous-agent"]

HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 \
    CMD curl --fail --silent http://127.0.0.1:7776/ok >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/agentd"]
CMD ["--config", "/etc/nous-agent/config.yaml"]
