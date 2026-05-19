import { create } from "zustand";

import type { User } from "./types";

interface AuthState {
    token: string | null;
    user: User | null;
    isLoggedIn: boolean;
    hydrated: boolean;

    setAuth: (user: User, token?: string | null) => void;
    logout: () => void;
}

export const useAuthStore = create<AuthState>()((set) => ({
    token: null,
    user: null,
    isLoggedIn: true,
    hydrated: true,

    setAuth: (_user: User, _token: string | null = null) =>
        set({}),

    logout: () =>
        set({}),
}));

/**
 * Get the current auth token (non-reactive, for use outside React).
 */
export function getAuthToken(): string | null {
    return useAuthStore.getState().token;
}
