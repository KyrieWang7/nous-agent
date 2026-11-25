"use client";

import { useEffect } from "react";
import { getSession } from "./api";
import { useAuthStore } from "./store";

export function useAuthBootstrap(): void {
  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const session = await getSession(controller.signal);
        if (!controller.signal.aborted) useAuthStore.getState().setSession(session);
      } catch {
        if (!controller.signal.aborted) {
          useAuthStore.getState().setSession({ enabled: true, user: null });
        }
      }
    };
    void load();
    const refreshFromCache = (event: PageTransitionEvent) => {
      if (event.persisted) void load();
    };
    window.addEventListener("pageshow", refreshFromCache);
    return () => {
      controller.abort();
      window.removeEventListener("pageshow", refreshFromCache);
    };
  }, []);
}

export function useAuthHydrated(): boolean {
  return useAuthStore((state) => state.hydrated);
}
