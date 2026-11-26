import { env } from "@/env";

const basePath = process.env.NEXT_PUBLIC_NOUS_BASE_PATH || "";

export function webPath(path: string) {
  return path.startsWith("/") && !path.startsWith("//") ? `${basePath}${path}` : path;
}

export function getBackendBaseURL() {
  if (env.NEXT_PUBLIC_BACKEND_BASE_URL) {
    return env.NEXT_PUBLIC_BACKEND_BASE_URL;
  } else {
    return basePath;
  }
}

export function getAgentAPIBaseURL() {
  if (env.NEXT_PUBLIC_AGENT_API_BASE_URL) {
    return env.NEXT_PUBLIC_AGENT_API_BASE_URL;
  } else {
    if (typeof window !== "undefined") {
      return `${window.location.origin}${webPath("/api/agent")}`;
    }
    // Fallback for SSR; local development uses the same-origin Next.js proxy.
    return `http://localhost:7775${webPath("/api/agent")}`;
  }
}
