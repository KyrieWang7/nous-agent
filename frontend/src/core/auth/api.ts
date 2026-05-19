import type { LoginRequest, RegisterRequest, TokenResponse, User } from "./types";

export async function login(_data: LoginRequest): Promise<TokenResponse> {
    return { access_token: "", token_type: "bearer" } as TokenResponse;
}

export async function register(_data: RegisterRequest): Promise<TokenResponse> {
    return { access_token: "", token_type: "bearer" } as TokenResponse;
}

export async function logout(): Promise<void> {
    // No-op
}

export async function getMe(_token?: string | null): Promise<User> {
    return { id: "local", email: "local@nous-agent", username: "user" } as User;
}
