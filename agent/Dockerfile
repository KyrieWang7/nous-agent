# Nous Agent - LangGraph Server Dockerfile

FROM python:3.12-slim

# Install system dependencies
RUN apt-get update && apt-get install -y \
    curl \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

# Install uv
RUN curl -LsSf https://astral.sh/uv/install.sh | sh
ENV PATH="/root/.local/bin:$PATH"

# Set working directory
WORKDIR /app

# Copy dependency files
COPY pyproject.toml uv.lock ./

# Install dependencies with cache mount
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync

# Copy source code
COPY src/ ./src/
COPY scripts/ ./scripts/

# Expose port (agent: 2024)
EXPOSE 2024

# Default command
CMD ["uv", "run", "uvicorn", "src.server:app", "--host", "0.0.0.0", "--port", "2024"]