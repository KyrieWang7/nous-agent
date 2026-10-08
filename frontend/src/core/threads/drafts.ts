interface DraftStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

const draftKey = (threadId: string) => `nous.draft.${threadId}`;

export function readDraft(
  storage: DraftStorage | undefined,
  threadId: string,
  fallback = "",
): string {
  try {
    const raw = storage?.getItem(draftKey(threadId));
    if (!raw) return fallback;
    const draft: unknown = JSON.parse(raw);
    if (
      draft &&
      typeof draft === "object" &&
      "text" in draft &&
      "version" in draft &&
      draft.version === 1 &&
      typeof draft.text === "string"
    ) {
      return draft.text;
    }
  } catch {
    // Storage denial or damaged local data must not prevent composing a message.
  }
  return fallback;
}

export function writeDraft(
  storage: DraftStorage | undefined,
  threadId: string,
  text: string,
): void {
  try {
    if (text)
      storage?.setItem(
        draftKey(threadId),
        JSON.stringify({ version: 1, text }),
      );
    else storage?.removeItem(draftKey(threadId));
  } catch {
    // The in-memory input remains usable when browser storage is unavailable.
  }
}

export function browserDraftStorage(): DraftStorage | undefined {
  try {
    return typeof window === "undefined" ? undefined : window.localStorage;
  } catch {
    return undefined;
  }
}
