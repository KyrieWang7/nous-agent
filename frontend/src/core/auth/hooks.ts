"use client";

import { useAuthStore } from "./store";

export function useAuthBootstrap(): void {
  // No-op: auth is disabled in open-source mode
}

export function useAuthHydrated(): boolean {
  return useAuthStore((state) => state.hydrated);
}
