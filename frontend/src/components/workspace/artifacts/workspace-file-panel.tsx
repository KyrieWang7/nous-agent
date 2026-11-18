"use client";

import { XIcon } from "lucide-react";
import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { useI18n } from "@/core/i18n/hooks";
import { cn } from "@/lib/utils";

import { ArtifactFileDetail } from "./artifact-file-detail";
import { useArtifacts } from "./context";
import { WorkspaceFiles } from "./workspace-files";

function fileLabel(path: string) {
  return path.startsWith("write-file:")
    ? new URL(path).pathname.split("/").at(-1)
    : path.split("/").at(-1);
}

export function WorkspaceFilePanel({
  threadId,
  published,
}: {
  threadId: string;
  published: string[];
}) {
  const { t } = useI18n();
  const { selectedArtifact, select, deselect } = useArtifacts();
  const key = `nous.workspace.files.${threadId}`;
  const [tabs, setTabs] = useState<string[]>([]);
  const [loaded, setLoaded] = useState(false);
  useEffect(() => {
    try {
      const value: unknown = JSON.parse(sessionStorage.getItem(key) ?? "[]");
      if (Array.isArray(value)) {
        setTabs(
          value.filter(
            (path): path is string =>
              typeof path === "string" &&
              (path.startsWith("/") || path.startsWith("write-file:/")),
          ),
        );
      }
    } catch {
      // The explorer works without browser storage.
    }
    setLoaded(true);
  }, [key]);
  useEffect(() => {
    if (!loaded || !selectedArtifact) return;
    setTabs((previous) =>
      previous.includes(selectedArtifact)
        ? previous
        : [...previous, selectedArtifact],
    );
  }, [loaded, selectedArtifact]);
  useEffect(() => {
    if (!loaded) return;
    try {
      sessionStorage.setItem(key, JSON.stringify(tabs));
    } catch {
      // File tabs remain available in memory.
    }
  }, [key, loaded, tabs]);
  return (
    <div className="flex size-full min-h-0 flex-col">
      {tabs.length > 0 && (
        <div className="flex shrink-0 items-center gap-1 overflow-x-auto border-b px-2 py-1">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={deselect}
            aria-pressed={!selectedArtifact}
          >
            {t.workbenchPanel.backToFiles}
          </Button>
          {tabs.map((path) => (
            <div
              key={path}
              className={cn(
                "flex shrink-0 items-center rounded-md border",
                path === selectedArtifact && "bg-muted",
              )}
            >
              <Button
                type="button"
                variant="ghost"
                size="sm"
                aria-pressed={path === selectedArtifact}
                title={path}
                onClick={() => select(path)}
              >
                <span className="max-w-36 truncate">{fileLabel(path)}</span>
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={`${t.common.close} ${fileLabel(path)}`}
                onClick={() => {
                  setTabs((previous) =>
                    previous.filter((file) => file !== path),
                  );
                  if (path === selectedArtifact) deselect();
                }}
              >
                <XIcon className="size-3" />
              </Button>
            </div>
          ))}
        </div>
      )}
      <div className="min-h-0 flex-1">
        {selectedArtifact ? (
          <ArtifactFileDetail
            key={selectedArtifact}
            className="size-full"
            filepath={selectedArtifact}
            threadId={threadId}
          />
        ) : (
          <WorkspaceFiles threadId={threadId} published={published} />
        )}
      </div>
    </div>
  );
}
