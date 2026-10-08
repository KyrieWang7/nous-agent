"use client";

import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { ListFilterIcon, RefreshCwIcon } from "lucide-react";
import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { useI18n } from "@/core/i18n/hooks";
import {
  fetchInspectableRuns,
  fetchRunEvents,
  type RuntimeEvent,
} from "@/core/threads/run-events";

import { Tooltip } from "./tooltip";

export function RunEventInspector({ threadId }: { threadId: string }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Tooltip content={t.inspector.title}>
        <DialogTrigger asChild>
          <Button variant="ghost" size="icon" aria-label={t.inspector.title}>
            <ListFilterIcon className="size-4" />
          </Button>
        </DialogTrigger>
      </Tooltip>
      <DialogContent
        aria-describedby={undefined}
        className="flex h-[80dvh] max-h-[850px] min-h-0 flex-col sm:max-w-4xl"
      >
        <DialogHeader>
          <DialogTitle>{t.inspector.title}</DialogTitle>
        </DialogHeader>
        {open && <InspectorContent key={threadId} threadId={threadId} />}
      </DialogContent>
    </Dialog>
  );
}

function InspectorContent({ threadId }: { threadId: string }) {
  const { t } = useI18n();
  const [selectedRun, setSelectedRun] = useState<string | null>(null);
  const runs = useQuery({
    queryKey: ["inspectable-runs", threadId],
    queryFn: ({ signal }) => fetchInspectableRuns(threadId, signal),
  });
  const orderedRuns = useMemo(
    () =>
      [...(runs.data ?? [])].sort((a, b) =>
        b.created_at.localeCompare(a.created_at),
      ),
    [runs.data],
  );
  const runId = selectedRun ?? orderedRuns[0]?.run_id ?? "";
  return (
    <>
      <label className="flex min-w-0 items-center gap-3 text-sm">
        {t.inspector.run}
        <select
          aria-label={t.inspector.run}
          value={runId}
          onChange={(event) => setSelectedRun(event.target.value)}
          className="bg-background min-w-0 flex-1 rounded-md border p-2 font-mono text-xs"
          disabled={!orderedRuns.length}
        >
          {!orderedRuns.length && (
            <option value="">
              {runs.isLoading ? t.common.loading : t.inspector.noRuns}
            </option>
          )}
          {orderedRuns.map((run) => (
            <option key={run.run_id} value={run.run_id}>
              {run.run_id} · {run.status}
            </option>
          ))}
        </select>
        <Tooltip content={t.inspector.refresh}>
          <Button
            variant="ghost"
            size="icon"
            aria-label={t.inspector.refresh}
            disabled={runs.isFetching}
            onClick={() => void runs.refetch()}
          >
            <RefreshCwIcon className="size-4" />
          </Button>
        </Tooltip>
      </label>
      {runs.isError && (
        <p role="alert" className="text-destructive text-sm">
          {t.inspector.failed}
        </p>
      )}
      {runId && <EventTable key={runId} threadId={threadId} runId={runId} />}
    </>
  );
}

function EventTable({ threadId, runId }: { threadId: string; runId: string }) {
  const { t } = useI18n();
  const [type, setType] = useState("");
  const [selected, setSelected] = useState<RuntimeEvent | null>(null);
  const query = useInfiniteQuery({
    queryKey: ["run-events", threadId, runId],
    queryFn: ({ pageParam, signal }) =>
      fetchRunEvents(threadId, runId, pageParam, signal),
    initialPageParam: 0,
    getNextPageParam: (page) => (page.has_more ? page.next_after : undefined),
  });
  const events = useMemo(
    () => query.data?.pages.flatMap((page) => page.events) ?? [],
    [query.data],
  );
  const types = useMemo(
    () => [...new Set(events.map((event) => event.type))].sort(),
    [events],
  );
  const visible = type ? events.filter((event) => event.type === type) : events;
  return (
    <>
      <div className="flex items-center gap-2">
        <select
          aria-label={t.inspector.eventType}
          className="bg-background min-w-0 flex-1 rounded-md border p-2 text-sm"
          value={type}
          onChange={(event) => setType(event.target.value)}
        >
          <option value="">{t.inspector.allTypes}</option>
          {types.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </select>
        <Tooltip content={t.inspector.refresh}>
          <Button
            variant="ghost"
            size="icon"
            aria-label={t.inspector.refresh}
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
          >
            <RefreshCwIcon className="size-4" />
          </Button>
        </Tooltip>
      </div>
      {query.isError && (
        <p role="alert" className="text-destructive text-sm">
          {t.inspector.failed}
        </p>
      )}
      <div className="min-h-0 flex-1 overflow-auto rounded-md border">
        <table className="w-full text-left text-xs">
          <thead className="bg-background sticky top-0">
            <tr>
              <th className="p-2">{t.inspector.sequence}</th>
              <th className="p-2">{t.inspector.eventType}</th>
              <th className="p-2">{t.inspector.time}</th>
            </tr>
          </thead>
          <tbody>
            {visible.map((event) => (
              <tr
                key={event.seq}
                className={
                  selected?.seq === event.seq ? "bg-muted" : "hover:bg-muted/50"
                }
              >
                <td className="p-2 tabular-nums">{event.seq}</td>
                <td className="max-w-48 p-2">
                  <button
                    type="button"
                    className="max-w-full text-left font-mono break-all underline-offset-4 hover:underline"
                    onClick={() => setSelected(event)}
                  >
                    {event.type}
                  </button>
                </td>
                <td className="p-2 whitespace-nowrap tabular-nums">
                  {new Date(event.created_at).toLocaleTimeString()}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {!visible.length && (
          <p className="text-muted-foreground p-4 text-sm">
            {query.isLoading ? t.common.loading : t.inspector.empty}
          </p>
        )}
      </div>
      <div className="flex shrink-0 justify-end">
        <Button
          variant="outline"
          size="sm"
          disabled={!query.hasNextPage || query.isFetching}
          onClick={() => void query.fetchNextPage()}
        >
          {t.inspector.loadMore}
        </Button>
      </div>
      {selected && (
        <pre
          tabIndex={0}
          className="bg-muted max-h-48 min-h-20 shrink-0 overflow-auto rounded-md p-3 text-xs"
        >
          {JSON.stringify(selected, null, 2)}
        </pre>
      )}
    </>
  );
}
