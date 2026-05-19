"use client";

import { useAuthBootstrap } from "@/core/auth";

export function SessionBootstrap() {
  useAuthBootstrap();
  return null;
}
