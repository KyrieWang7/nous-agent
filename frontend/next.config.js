/**
 * Run `build` or `dev` with `SKIP_ENV_VALIDATION` to skip env validation.
 * This is especially useful for Docker builds.
 */
import "./src/env.js";

/** @type {import("next").NextConfig} */
const gatewayUrl =
  process.env.NEXT_PUBLIC_BACKEND_BASE_URL ?? "http://127.0.0.1:7777";
const langgraphUrl =
  process.env.NEXT_PUBLIC_LANGGRAPH_BASE_URL ?? "http://127.0.0.1:7776";

const config = {
  devIndicators: false,
  async rewrites() {
    return [
      // LangGraph Agent API — must be matched before the general /api/* rule
      {
        source: "/api/langgraph/:path*",
        destination: `${langgraphUrl}/:path*`,
      },
      // Gateway API (models, MCP, skills, memory, uploads, artifacts)
      {
        source: "/api/:first((?!drama$|langgraph$)[^/]+)",
        destination: `${gatewayUrl}/api/:first`,
      },
      {
        source: "/api/:first((?!drama$|langgraph$)[^/]+)/:rest*",
        destination: `${gatewayUrl}/api/:first/:rest*`,
      },
    ];
  },
};

export default config;
