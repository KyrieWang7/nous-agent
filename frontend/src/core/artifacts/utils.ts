import { getBackendBaseURL } from "../config";
import type { AgentThread } from "../threads";

export function urlOfArtifact({
  filepath,
  threadId,
  download = false,
}: {
  filepath: string;
  threadId: string;
  download?: boolean;
}) {
  const encoded = filepath
    .replace(/^\/+/, "")
    .split("/")
    .map(encodeURIComponent)
    .join("/");
  return `${getBackendBaseURL()}/api/threads/${encodeURIComponent(threadId)}/artifacts/${encoded}${download ? "?download=true" : ""}`;
}

export function extractArtifactsFromThread(thread: AgentThread) {
  return thread.values.artifacts ?? [];
}

export function resolveArtifactURL(absolutePath: string, threadId: string) {
  return urlOfArtifact({ filepath: absolutePath, threadId });
}
