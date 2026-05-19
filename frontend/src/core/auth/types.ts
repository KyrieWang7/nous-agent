export interface User {
    id: string;
    username: string;
    display_name: string | null;
    email: string | null;
    avatar_url: string | null;
}

export interface TokenResponse {
    access_token: string;
    token_type: string;
    user_id: string;
    username: string;
    display_name: string | null;
}

export interface LoginRequest {
    email: string;
    password: string;
}

export interface RegisterRequest {
    email: string;
    password: string;
    display_name?: string;
}
