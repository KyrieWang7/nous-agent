export interface User {
  id: string;
  username: string;
  displayName?: string;
  email?: string;
  role: string;
}

export interface Session {
  enabled: boolean;
  user: User | null;
}
