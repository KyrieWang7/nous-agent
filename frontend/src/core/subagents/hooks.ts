"use client";

import { useQuery } from "@tanstack/react-query";

import { childSessionKey, listChildren } from "./api";
import { isChildActive } from "./types";

export function useChildCatalog(
  threadId: string | null,
  enabled: boolean,
  busy: boolean,
) {
  return useQuery({
    queryKey: childSessionKey(threadId ?? ""),
    queryFn: ({ signal }) => listChildren(threadId!, signal),
    enabled: !!threadId && enabled,
    refetchInterval: (query) =>
      busy || query.state.data?.some((s) => isChildActive(s.result.status))
        ? 1500
        : 5000,
    refetchOnWindowFocus: true,
    retry: 1,
  });
}
