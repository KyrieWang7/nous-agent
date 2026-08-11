export function isThreadNotFoundError(error: unknown): boolean {
  return (
    error instanceof Error && /getState failed:\s*404\b/.test(error.message)
  );
}
