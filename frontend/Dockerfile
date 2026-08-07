# Frontend Development Dockerfile
FROM node:22-alpine

# Build arguments for environment variables
ARG NEXT_PUBLIC_BACKEND_BASE_URL
ENV NEXT_PUBLIC_BACKEND_BASE_URL=${NEXT_PUBLIC_BACKEND_BASE_URL}
ARG NEXT_PUBLIC_LANGGRAPH_BASE_URL
ENV NEXT_PUBLIC_LANGGRAPH_BASE_URL=${NEXT_PUBLIC_LANGGRAPH_BASE_URL}

# Accept build argument for pnpm store path
ARG PNPM_STORE_PATH=/root/.local/share/pnpm/store

# Install pnpm at specific version (matching package.json)
RUN corepack enable && corepack install -g pnpm@10.26.2

RUN pnpm config set store-dir ${PNPM_STORE_PATH}

# Set working directory
WORKDIR /app

# Copy frontend source code
COPY . .

# Install dependencies
RUN pnpm install --no-frozen-lockfile

# Expose Next.js dev server port
EXPOSE 7775

CMD ["pnpm", "dev", "--hostname", "0.0.0.0"]
