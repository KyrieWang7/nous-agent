"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState, useSyncExternalStore } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { useI18n } from "@/core/i18n/hooks";
import {
  cancelSessionInput,
  changeSessionInput,
  inboxKey,
  isInputSending,
  listSessionInputs,
  pendingInputSnapshot,
  recoverSessionInput,
  subscribePendingInput,
  type SessionInput,
} from "@/core/threads/inbox";

export function useSessionInbox(tid: string | null, enabled: boolean) {
  return useQuery({
    queryKey: inboxKey(tid ?? ""),
    enabled: !!tid && enabled,
    queryFn: ({ signal }) => listSessionInputs(tid!, signal),
    refetchInterval: 1500,
    retry: false,
  });
}
export function SessionInbox({
  threadId,
  activeRunId,
  entries,
  error,
  onRefresh,
  onRecovered,
}: {
  threadId: string;
  activeRunId?: string | null;
  entries: SessionInput[];
  error: Error | null;
  onRefresh: () => void;
  onRecovered: (draft: string, attachmentIds?: string[]) => void;
}) {
  const { t } = useI18n();
  const copy = t.sessionInbox;
  const client = useQueryClient();
  const [edit, setEdit] = useState<{
    id: string;
    revision: string;
    prompt: string;
  } | null>(null);
  const refresh = () =>
    client.invalidateQueries({ queryKey: inboxKey(threadId) });
  const change = useMutation({
    mutationFn: ({
      entry,
      update,
    }: {
      entry: Pick<SessionInput, "id" | "updated_at">;
      update: { prompt: string } | { target_run_id: string };
    }) => changeSessionInput(threadId, entry.id, entry.updated_at, update),
    onSuccess: (_entry, { update }) => {
      if ("prompt" in update) setEdit(null);
    },
    onError: (error) => toast.error(error.message),
    onSettled: refresh,
  });
  const raw = useSyncExternalStore(
    subscribePendingInput,
    () => pendingInputSnapshot(threadId),
    () => null,
  );
  const sending = useSyncExternalStore(
    subscribePendingInput,
    () => isInputSending(threadId),
    () => false,
  );
  const visible = useMemo(
    () =>
      entries.filter(
        (e, i) =>
          e.status === "pending" ||
          e.status === "claimed" ||
          i >= entries.length - 4,
      ),
    [entries],
  );
  const cancel = useMutation({
    mutationFn: (id: string) => cancelSessionInput(threadId, id),
    retry: false,
    onSuccess: () => client.invalidateQueries({ queryKey: inboxKey(threadId) }),
    onError: (e) => toast.error(e.message),
  });
  const recover = useMutation({
    mutationFn: () => recoverSessionInput(threadId),
    retry: false,
    onSuccess: async (result) => {
      if (result) onRecovered(result.draft, result.attachmentIds);
      await client.invalidateQueries({ queryKey: inboxKey(threadId) });
    },
    onError: (e) => toast.error(e.message),
  });
  if (!visible.length && !raw && !error) return null;
  return (
    <section
      aria-label={copy.title}
      className="bg-muted/25 mb-2 max-h-48 overflow-y-auto rounded-lg border px-3 py-2 text-xs"
    >
      <div className="mb-1 flex items-center justify-between gap-2">
        <span className="font-medium">{copy.title}</span>
        <Button type="button" size="sm" variant="ghost" onClick={onRefresh}>
          {t.subagentWorkspace.refresh}
        </Button>
      </div>
      {error && (
        <p role="alert" className="text-destructive mb-2">
          {copy.loadError} · {error.message}
        </p>
      )}
      {raw && sending && (
        <p className="mb-2" role="status">
          {t.subagentWorkspace.sending}
        </p>
      )}
      {raw && !sending && (
        <div role="alert" className="mb-2 flex items-center gap-2">
          <span className="min-w-0 flex-1">{copy.unconfirmed}</span>
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={recover.isPending}
            onClick={() => recover.mutate()}
          >
            {copy.recover}
          </Button>
        </div>
      )}
      <ol className="space-y-1">
        {visible.map((e) => {
          const editing = edit?.id === e.id ? edit : null;
          const stale =
            !!editing &&
            (e.status !== "pending" || e.updated_at !== editing.revision);
          const textOnly =
            !e.input.content_blocks?.length &&
            !e.input.prompt.includes("<uploaded_files>");
          return (
            <li
              key={e.id}
              data-inbox-id={e.id}
              className="flex flex-wrap items-start gap-2 border-t py-1.5"
            >
              <span className="text-muted-foreground shrink-0">
                {e.delivery === "next_step" ? copy.nextStep : copy.nextTurn}
              </span>
              <span className="line-clamp-2 min-w-0 flex-1 break-words whitespace-pre-wrap">
                {e.input.prompt || copy.attachment}
              </span>
              <span className="text-muted-foreground shrink-0" title={e.error}>
                {copy.statuses[e.status]}
                {e.status === "admitted" && e.run_status
                  ? ` · ${t.subagentWorkspace.statuses[({ success: "completed", error: "failed" }[e.run_status] ?? e.run_status) as keyof typeof t.subagentWorkspace.statuses] ?? e.run_status}`
                  : ""}
              </span>
              {e.status === "pending" && !editing && (
                <div className="flex flex-wrap gap-1">
                  {textOnly && (
                    <Button
                      type="button"
                      size="sm"
                      variant="ghost"
                      disabled={change.isPending || cancel.isPending}
                      onClick={() =>
                        setEdit({
                          id: e.id,
                          revision: e.updated_at,
                          prompt: e.input.prompt,
                        })
                      }
                    >
                      {copy.edit}
                    </Button>
                  )}
                  {e.delivery === "next_turn" && activeRunId && (
                    <Button
                      type="button"
                      size="sm"
                      variant="ghost"
                      disabled={change.isPending || cancel.isPending}
                      onClick={() =>
                        change.mutate({
                          entry: e,
                          update: { target_run_id: activeRunId },
                        })
                      }
                    >
                      {copy.steer}
                    </Button>
                  )}
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    disabled={cancel.isPending || change.isPending}
                    onClick={() => cancel.mutate(e.id)}
                  >
                    {copy.withdraw}
                  </Button>
                </div>
              )}
              {editing && (
                <form
                  className="w-full space-y-2"
                  onSubmit={(event) => {
                    event.preventDefault();
                    if (stale || !editing.prompt.trim() || change.isPending)
                      return;
                    change.mutate({
                      entry: { id: e.id, updated_at: editing.revision },
                      update: { prompt: editing.prompt },
                    });
                  }}
                >
                  <Textarea
                    aria-label={copy.edit}
                    value={editing.prompt}
                    autoFocus
                    disabled={change.isPending}
                    onChange={(event) =>
                      setEdit({ ...editing, prompt: event.target.value })
                    }
                  />
                  {stale && <p role="alert">{copy.changed}</p>}
                  <div className="flex justify-end gap-2">
                    <Button
                      type="button"
                      size="sm"
                      variant="ghost"
                      disabled={change.isPending}
                      onClick={() => setEdit(null)}
                    >
                      {t.common.close}
                    </Button>
                    <Button
                      type="submit"
                      size="sm"
                      disabled={
                        stale || change.isPending || !editing.prompt.trim()
                      }
                    >
                      {copy.save}
                    </Button>
                  </div>
                </form>
              )}
            </li>
          );
        })}
      </ol>
    </section>
  );
}
