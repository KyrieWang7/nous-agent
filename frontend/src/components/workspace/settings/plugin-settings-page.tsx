"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { getAgentAPIBaseURL } from "@/core/config";
import { useI18n } from "@/core/i18n/hooks";

import { SettingsSection } from "./settings-section";

interface PluginSnapshot {
  revision: string;
  enabled: boolean;
  load_error?: string;
  catalog: {
    name: string;
    version: string;
    description?: string;
    tools: string[];
  }[];
  plugins: {
    name: string;
    version?: string;
    installed?: boolean;
    description?: string;
    tools: string[];
    enabled: boolean;
    loaded: boolean;
    retained_runs: number;
    close_error?: string;
  }[];
}
const key = ["runtime-plugins"] as const;
async function request(
  path = "",
  options?: RequestInit,
): Promise<PluginSnapshot> {
  const r = await fetch(`${getAgentAPIBaseURL()}/runtime/plugins${path}`, {
    ...options,
    credentials: "include",
    cache: "no-store",
    headers: { "Content-Type": "application/json" },
  });
  if (!r.ok) {
    let detail = await r.text();
    try {
      detail = (JSON.parse(detail) as { detail?: string }).detail ?? detail;
    } catch {
      /* plain response */
    }
    throw new Error(`${r.status}: ${detail}`);
  }
  return r.json() as Promise<PluginSnapshot>;
}
export function PluginSettingsPage() {
  const { t } = useI18n();
  const text = t.pluginSettings;
  const client = useQueryClient();
  const [view, setView] = useState<"installed" | "discover">("installed");
  const [confirmRemove, setConfirmRemove] = useState<string | null>(null);
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => request("", { signal }),
    refetchInterval: 5000,
    retry: false,
  });
  const change = useMutation({
    mutationFn: (value: {
      name?: string;
      enabled?: boolean;
      reload?: boolean;
      action?: "install" | "uninstall";
      version?: string;
    }) =>
      request(value.reload ? "/reload" : "", {
        method: "POST",
        body: JSON.stringify({
          ...(value.reload
            ? {}
            : {
                name: value.name,
                enabled: value.enabled,
                action: value.action,
                version: value.version,
              }),
          revision: query.data?.revision,
        }),
      }),
    retry: false,
    onSuccess: (snapshot) => {
      client.setQueryData(key, snapshot);
      setConfirmRemove(null);
    },
    onError: (error) => {
      toast.error(error.message);
      void client.invalidateQueries({ queryKey: key });
    },
  });
  const snapshot = query.data;
  const disabled = change.isPending || query.isError || !snapshot;
  return (
    <SettingsSection title={text.title} description={text.description}>
      <div className="flex gap-2">
        <Button
          type="button"
          variant={view === "installed" ? "secondary" : "ghost"}
          aria-pressed={view === "installed"}
          onClick={() => setView("installed")}
        >
          {text.installed}
        </Button>
        <Button
          type="button"
          variant={view === "discover" ? "secondary" : "ghost"}
          aria-pressed={view === "discover"}
          onClick={() => setView("discover")}
        >
          {text.discover}
        </Button>
      </div>
      {view === "installed" && (
        <>
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border p-4">
            <div className="min-w-0 flex-1">
              <p className="font-medium">{text.host}</p>
              <p className="text-muted-foreground mt-1 text-sm">
                {text.hostHint}
              </p>
            </div>
            <Switch
              aria-label={text.host}
              checked={snapshot?.enabled ?? false}
              disabled={disabled}
              onCheckedChange={(enabled) => change.mutate({ enabled })}
            />
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              variant="outline"
              disabled={disabled}
              onClick={() => change.mutate({ reload: true })}
            >
              {change.isPending ? text.applying : text.reload}
            </Button>
            <Button
              type="button"
              variant="ghost"
              disabled={query.isFetching}
              onClick={() => {
                void query.refetch();
              }}
            >
              {t.subagentWorkspace.refresh}
            </Button>
          </div>
        </>
      )}
      {query.isLoading && (
        <p className="text-muted-foreground text-sm">{t.common.loading}</p>
      )}
      {query.error && (
        <p role="alert" className="text-destructive text-sm break-words">
          {query.error.message}
        </p>
      )}
      {snapshot?.load_error && (
        <p
          role="alert"
          className="text-destructive rounded-lg border p-3 text-sm break-words"
        >
          {text.loadFailed} {snapshot.load_error}
        </p>
      )}
      {view === "installed" && snapshot && !snapshot.plugins.length && (
        <p className="text-muted-foreground rounded-lg border border-dashed p-6 text-sm">
          {text.empty}
        </p>
      )}
      {view === "installed" && (
        <ul className="space-y-3">
          {snapshot?.plugins.map((p) => (
            <li key={p.name} className="space-y-2 rounded-lg border p-4">
              <div className="flex items-center justify-between gap-3">
                <div className="min-w-0">
                  <h3 className="font-medium break-words">
                    {p.name}{" "}
                    {p.version && (
                      <span className="text-muted-foreground text-xs">
                        {p.version}
                      </span>
                    )}
                  </h3>
                  <p className="text-muted-foreground mt-1 text-xs">
                    {p.close_error
                      ? text.closeFailed
                      : p.loaded
                        ? text.loaded
                        : p.retained_runs > 0
                          ? text.draining
                          : p.enabled && !snapshot.enabled
                            ? text.waiting
                            : text.disabled}
                  </p>
                </div>
                <Switch
                  aria-label={`${text.toggle} ${p.name}`}
                  disabled={disabled || p.installed === false}
                  checked={p.enabled}
                  onCheckedChange={(enabled) =>
                    change.mutate({ name: p.name, enabled })
                  }
                />
              </div>
              {p.description && (
                <p className="text-muted-foreground text-sm break-words">
                  {p.description}
                </p>
              )}
              <div className="flex flex-wrap gap-1.5">
                {p.tools.map((tool) => (
                  <code
                    key={tool}
                    className="bg-muted rounded px-2 py-1 text-xs break-all"
                  >
                    {tool}
                  </code>
                ))}
              </div>
              {p.retained_runs > 0 && (
                <p role="status" className="text-muted-foreground text-sm">
                  {text.retained.replace("{count}", String(p.retained_runs))}
                </p>
              )}
              {p.close_error && (
                <p
                  role="alert"
                  className="text-destructive text-sm break-words"
                >
                  {p.close_error}
                </p>
              )}
              {p.version &&
                (confirmRemove === p.name ? (
                  <div className="flex flex-wrap items-center gap-2">
                    <p className="flex-1 text-sm">{text.confirmRemove}</p>
                    <Button
                      type="button"
                      variant="ghost"
                      disabled={change.isPending}
                      onClick={() => setConfirmRemove(null)}
                    >
                      {t.common.close}
                    </Button>
                    <Button
                      type="button"
                      variant="destructive"
                      disabled={disabled}
                      onClick={() =>
                        change.mutate({ name: p.name, action: "uninstall" })
                      }
                    >
                      {text.uninstall}
                    </Button>
                  </div>
                ) : (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={disabled}
                    onClick={() => setConfirmRemove(p.name)}
                  >
                    {text.uninstall}
                  </Button>
                ))}
            </li>
          ))}
        </ul>
      )}
      {view === "discover" && snapshot && (
        <ul className="space-y-3">
          {!snapshot.catalog?.length && (
            <li className="text-muted-foreground text-sm">{text.noCatalog}</li>
          )}
          {snapshot.catalog?.map((item) => {
            const installed = snapshot.plugins.find(
              (p) => p.name === item.name && p.installed !== false,
            );
            const sameVersion = installed?.version === item.version;
            const legacy = !!installed && !installed.version;
            return (
              <li
                key={`${item.name}/${item.version}`}
                className="flex flex-wrap items-center justify-between gap-3 rounded-lg border p-4"
              >
                <div className="min-w-0 flex-1">
                  <h3 className="font-medium break-words">
                    {item.name}{" "}
                    <span className="text-muted-foreground text-xs">
                      {item.version}
                    </span>
                  </h3>
                  {item.description && (
                    <p className="text-muted-foreground text-sm">
                      {item.description}
                    </p>
                  )}
                </div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={disabled || sameVersion || legacy}
                  onClick={() =>
                    change.mutate({
                      name: item.name,
                      version: item.version,
                      action: "install",
                    })
                  }
                >
                  {sameVersion || legacy
                    ? text.installed
                    : installed
                      ? text.switchVersion
                      : text.install}
                </Button>
              </li>
            );
          })}
        </ul>
      )}
    </SettingsSection>
  );
}
