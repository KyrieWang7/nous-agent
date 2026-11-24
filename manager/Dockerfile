# syntax=docker/dockerfile:1
FROM node:24-bookworm-slim AS web
WORKDIR /build/web/frontend
COPY third_party/easel/web/frontend/package*.json ./
RUN npm ci --no-audit --no-fund
COPY third_party/easel/web/frontend/ ./
COPY third_party/easel/web/static/dropdown.css /build/web/static/dropdown.css
RUN npm run build

FROM golang:1.25-alpine AS api
WORKDIR /src/manager/manager_service
COPY platform-go/ /src/platform-go/
COPY manager/manager_service/go.mod manager/manager_service/go.sum ./
RUN go mod download
COPY manager/manager_service/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/manager ./cmd

FROM ghcr.io/astral-sh/uv:0.13.9 AS uv

FROM python:3.12-bookworm
COPY --from=web /usr/local/bin/node /usr/local/bin/node
COPY --from=web /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx

ENV PLAYWRIGHT_BROWSERS_PATH=/opt/playwright \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    MANAGER_HTTP_ADDR=0.0.0.0:7860

COPY third_party/easel/pyproject.toml /build/easel/
COPY third_party/easel/easel/ /build/easel/easel/
# Use the Bilibili CLI wheels available for both target Linux architectures.
RUN --mount=type=bind,from=uv,source=/uv,target=/usr/local/bin/uv \
    --mount=type=cache,target=/root/.cache/uv \
    uv pip install --system --link-mode copy --only-binary=biliup /build/easel 'biliup==1.2.9' 'rembg[cpu]>=2,<3'

RUN python -m playwright install --with-deps chromium \
    && apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg fonts-noto-cjk fonts-dejavu-core xvfb \
    && apt-get clean \
    && addgroup --system manager \
    && adduser --system --ingroup manager --home /var/lib/manager manager \
    && mkdir -p /var/lib/manager \
    && chown manager:manager /var/lib/manager

COPY third_party/easel /opt/manager/easel
COPY --from=web /build/web/frontend/dist /opt/manager/easel/web/frontend/dist
COPY --from=api /out/manager /usr/local/bin/manager
COPY manager/legacy_app.py /opt/manager/runtime/legacy_app.py
COPY --chmod=0555 manager/start.sh /usr/local/bin/nous-manager

USER manager
EXPOSE 7860
VOLUME ["/var/lib/manager"]
HEALTHCHECK --interval=15s --start-period=45s --timeout=5s \
    CMD python -c "from urllib.request import urlopen; assert urlopen('http://127.0.0.1:7860/health', timeout=3).status == 200"
ENTRYPOINT ["/usr/local/bin/nous-manager"]
