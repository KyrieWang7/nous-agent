import { create } from "zustand";

import type { Session } from "./types";

interface AuthState extends Session {
  isLoggedIn: boolean;
  hydrated: boolean;
  setSession: (session: Session) => void;
  logout: () => void;
}

export const useAuthStore = create<AuthState>()((set) => ({
  enabled: false,
  user: null,
  isLoggedIn: false,
  hydrated: false,

  setSession: (session) =>
    set({ ...session, isLoggedIn: !session.enabled || Boolean(session.user), hydrated: true }),

  logout: () => set({ user: null, isLoggedIn: false }),
}));
