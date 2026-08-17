import { env } from "@/env";

export function getBackendBaseURL() {
  if (env.NEXT_PUBLIC_BACKEND_BASE_URL) {
    return env.NEXT_PUBLIC_BACKEND_BASE_URL;
  } else {
    return "";
  }
}

export function getAgentAPIBaseURL() {
  if (env.NEXT_PUBLIC_AGENT_API_BASE_URL) {
    return env.NEXT_PUBLIC_AGENT_API_BASE_URL;
  } else {
    if (typeof window !== "undefined") {
      return `${window.location.origin}/api/agent`;
    }
    // Fallback for SSR; local development uses the same-origin Next.js proxy.
    return "http://localhost:7775/api/agent";
  }
}
