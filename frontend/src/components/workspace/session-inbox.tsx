"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRightIcon, ChevronDownIcon, PencilIcon, RotateCwIcon, XIcon } from "lucide-react";
import { useId, useState, useSyncExternalStore } from "react";
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
  const listId = useId();
  const [expanded, setExpanded] = useState(false);
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
  const visible = entries.filter((entry) => entry.status === "pending" || entry.status === "claimed");
  const showList = visible.length === 1 || expanded;
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
  if (!visible.length && (!raw || sending) && !error) return null;
  return (
    <section
      aria-label={copy.title}
      className="bg-muted/25 mb-2 max-h-[min(12rem,32dvh)] overflow-y-auto rounded-xl border px-2.5 py-1 text-xs"
    >
      {visible.length > 1 && (
        <Button
          type="button"
          size="sm"
          variant="ghost"
          aria-expanded={showList}
          aria-controls={listId}
          className="h-7 w-full justify-between px-1 text-xs"
          onClick={() => setExpanded(!expanded)}
        >
          {copy.queuedCount.replace("{count}", String(visible.length))}
          <ChevronDownIcon className={showList ? "rotate-180" : undefined} />
        </Button>
      )}
      {error && (
        <div className="flex items-center justify-between gap-2">
          <p role="alert" className="text-destructive min-w-0 break-words">
            {copy.loadError} · {error.message}
          </p>
          <Button type="button" size="icon-sm" variant="ghost" onClick={onRefresh} aria-label={t.subagentWorkspace.refresh} title={t.subagentWorkspace.refresh}>
            <RotateCwIcon />
          </Button>
        </div>
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
      <ol id={listId} hidden={!showList} className="divide-y">
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
              className="flex flex-wrap items-center gap-x-2 gap-y-1 py-1"
            >
              <span className="line-clamp-2 min-w-0 flex-1 break-words whitespace-pre-wrap" title={e.input.prompt}>
                {e.input.prompt || copy.attachment}
              </span>
              <span className="text-muted-foreground shrink-0">
                {e.status === "claimed" ? copy.statuses.claimed : e.delivery === "next_step" ? copy.nextStep : copy.statuses.pending}
              </span>
              {e.status === "pending" && !editing && (
                <div className="flex flex-wrap gap-1">
                  {textOnly && (
                    <Button
                      type="button"
                      size="icon-sm"
                      variant="ghost"
                      aria-label={copy.edit}
                      title={copy.edit}
                      disabled={change.isPending || cancel.isPending}
                      onClick={() =>
                        setEdit({
                          id: e.id,
                          revision: e.updated_at,
                          prompt: e.input.prompt,
                        })
                      }
                    >
                      <PencilIcon />
                    </Button>
                  )}
                  {e.delivery === "next_turn" && activeRunId && (
                    <Button
                      type="button"
                      size="icon-sm"
                      variant="ghost"
                      aria-label={copy.steer}
                      title={copy.steer}
                      disabled={change.isPending || cancel.isPending}
                      onClick={() =>
                        change.mutate({
                          entry: e,
                          update: { target_run_id: activeRunId },
                        })
                      }
                    >
                      <ArrowRightIcon />
                    </Button>
                  )}
                  <Button
                    type="button"
                    size="icon-sm"
                    variant="ghost"
                    aria-label={copy.withdraw}
                    title={copy.withdraw}
                    disabled={cancel.isPending || change.isPending}
                    onClick={() => cancel.mutate(e.id)}
                  >
                    <XIcon />
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
