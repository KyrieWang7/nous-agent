"use client";

import { useQuery } from "@tanstack/react-query";
import {
  ChevronRightIcon,
  FileIcon,
  FolderIcon,
  SearchIcon,
} from "lucide-react";
import { useDeferredValue, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { authFetch } from "@/core/api/auth-fetch";
import { getBackendBaseURL } from "@/core/config";
import { useI18n } from "@/core/i18n/hooks";

import { ArtifactFileList } from "./artifact-file-list";
import { useArtifacts } from "./context";

type FileEntry = { name: string; path: string; directory: boolean };
type Listing = { entries: FileEntry[]; truncated: boolean };

function useDirectory(
  threadId: string,
  path: string,
  query: string,
  enabled = true,
) {
  return useQuery({
    queryKey: ["workspace-files", threadId, path, query],
    enabled,
    queryFn: async ({ signal }) => {
      const params = new URLSearchParams({ path, q: query });
      const response = await authFetch(
        `${getBackendBaseURL()}/api/threads/${encodeURIComponent(threadId)}/files?${params}`,
        { signal },
      );
      if (!response.ok)
        throw new Error(`Could not list files (${response.status})`);
      return response.json() as Promise<Listing>;
    },
    retry: false,
    staleTime: 3000,
  });
}

function FilesList({
  threadId,
  path,
  query = "",
  enabled = true,
}: {
  threadId: string;
  path: string;
  query?: string;
  enabled?: boolean;
}) {
  const { t } = useI18n();
  const { select } = useArtifacts();
  const listing = useDirectory(threadId, path, query, enabled);
  if (!enabled) return null;
  if (listing.isPending)
    return (
      <p className="px-2 py-1 text-sm" role="status">
        {t.common.loading}
      </p>
    );
  if (listing.error)
    return (
      <div role="alert" className="px-2 text-sm">
        <p>
          {t.workbenchPanel.fileError} {listing.error.message}
        </p>
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => void listing.refetch()}
        >
          {t.subagentWorkspace.retry}
        </Button>
      </div>
    );
  return (
    <>
      {!listing.data.entries.length && (
        <p className="text-muted-foreground px-2 py-1 text-sm">
          {query ? t.workbenchPanel.noMatches : t.workbenchPanel.emptyDirectory}
        </p>
      )}
      <ul className="space-y-0.5">
        {listing.data.entries.map((entry) => (
          <li key={entry.path}>
            {entry.directory ? (
              <DirectoryRow
                entry={entry}
                threadId={threadId}
                showPath={!!query}
              />
            ) : (
              <button
                type="button"
                className="hover:bg-muted flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm"
                onClick={() => select(entry.path)}
                title={entry.path}
              >
                <FileIcon aria-hidden className="size-4 shrink-0" />
                <span className="min-w-0 truncate">
                  {query
                    ? entry.path.replace("/mnt/user-data/", "")
                    : entry.name}
                </span>
              </button>
            )}
          </li>
        ))}
      </ul>
      {listing.data.truncated && (
        <p role="status" className="text-muted-foreground px-2 text-sm">
          {t.workbenchPanel.searchLimit}
        </p>
      )}
    </>
  );
}

function DirectoryRow({
  entry,
  threadId,
  showPath,
}: {
  entry: FileEntry;
  threadId: string;
  showPath: boolean;
}) {
  const [open, setOpen] = useState(false);
  return (
    <details
      onToggle={(event) => setOpen(event.currentTarget.open)}
      className="group/folder"
    >
      <summary
        className="hover:bg-muted flex cursor-pointer list-none items-center gap-2 rounded-md px-2 py-1.5 text-sm"
        title={entry.path}
      >
        <ChevronRightIcon
          aria-hidden
          className="size-3 shrink-0"
          style={{ transform: open ? "rotate(90deg)" : undefined }}
        />
        <FolderIcon aria-hidden className="size-4 shrink-0" />
        <span className="min-w-0 truncate">
          {showPath ? entry.path.replace("/mnt/user-data/", "") : entry.name}
        </span>
      </summary>
      {open && (
        <div className="ml-3 border-l pl-2">
          <FilesList threadId={threadId} path={entry.path} />
        </div>
      )}
    </details>
  );
}

export function WorkspaceFiles({
  threadId,
  published,
}: {
  threadId: string;
  published: string[];
}) {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const search = useDeferredValue(query.trim());
  return (
    <div className="flex size-full min-h-0 flex-col">
      <div className="relative shrink-0 border-b p-3">
        <SearchIcon
          aria-hidden
          className="text-muted-foreground absolute top-5 left-5 size-4"
        />
        <Input
          type="search"
          className="pl-8"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          aria-label={t.workbenchPanel.searchFiles}
          placeholder={t.workbenchPanel.searchFiles}
        />
      </div>
      <div className="min-h-0 flex-1 space-y-3 overflow-auto p-3">
        {!search && published.length > 0 && (
          <section aria-label={t.common.artifacts}>
            <h3 className="mb-2 text-sm font-medium">{t.common.artifacts}</h3>
            <ArtifactFileList files={published} threadId={threadId} />
          </section>
        )}
        <FilesList key={search} threadId={threadId} path="" query={search} />
      </div>
    </div>
  );
}
