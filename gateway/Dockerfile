FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

RUN pip install --no-cache-dir uv

WORKDIR /app
COPY agent /app/agent
COPY gateway /app/gateway

WORKDIR /app/gateway
RUN uv sync --no-dev

EXPOSE 7777

CMD ["uv", "run", "uvicorn", "nous_gateway.app:app", "--host", "0.0.0.0", "--port", "7777"]
