"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { PlusIcon } from "lucide-react";
import { useId, useState, type FormEvent } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useI18n } from "@/core/i18n/hooks";
import { loadModelSettings, saveModelSettings } from "@/core/models/api";
import type { ModelConnection, ModelProvider } from "@/core/models/types";
import { env } from "@/env";

import { SettingsSection } from "./settings-section";

const providers = {
  "openai-compatible": "OpenAI compatible",
  anthropic: "Anthropic",
  deepseek: "DeepSeek",
} satisfies Record<ModelProvider, string>;

const settingsKey = ["model-settings"];

type Draft = {
  existingName?: string;
  name: string;
  displayName: string;
  provider: ModelProvider;
  modelId: string;
  baseURL: string;
  apiKey: string;
  makeDefault: boolean;
};

export function ModelSettingsPage() {
  const { t } = useI18n();
  const labels = t.settings.models;
  const providerId = useId();
  const queryClient = useQueryClient();
  const settings = useQuery({
    queryKey: settingsKey,
    queryFn: loadModelSettings,
  });
  const [draft, setDraft] = useState<Draft>();
  const [saved, setSaved] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const writable = env.NEXT_PUBLIC_STATIC_WEBSITE_ONLY !== "true";

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!draft || !writable || saving) return;
    const values = draft;
    setSaving(true);
    setSaveError("");
    try {
      const name = values.name.trim();
      const modelId = values.modelId.trim();
      if (!name || !modelId) throw new Error(labels.required);

      const latest = await loadModelSettings();
      const previous = latest.models.find((model) => model.name === name);
      if (values.existingName && !previous) throw new Error(labels.missing);
      if (!values.existingName && previous) throw new Error(labels.duplicate);

      const model: ModelConnection & { api_key?: string } = {
        ...previous,
        name,
        display_name: values.displayName.trim(),
        provider: values.provider,
        model: modelId,
        base_url: values.baseURL.trim(),
        ...(values.apiKey.trim() && { api_key: values.apiKey.trim() }),
      };
      if (previous && previous.provider !== model.provider) {
        if (model.provider !== "deepseek") delete model.use_files;
        if (model.provider === "deepseek") {
          delete model.extra_body;
          delete model.when_thinking_enabled;
        }
      }

      const updated = previous
        ? latest.models.map((item) => (item.name === name ? model : item))
        : [...latest.models, model];
      const defaultModel =
        values.makeDefault || !latest.default_model
          ? name
          : latest.default_model;
      const models = values.makeDefault
        ? [model, ...updated.filter((item) => item.name !== name)]
        : updated;
      const current = await saveModelSettings({
        default_model: defaultModel,
        models,
      });
      queryClient.setQueryData(settingsKey, current);
      void queryClient.invalidateQueries({ queryKey: ["models"] });
      setDraft(undefined);
      setSaved(true);
    } catch (error) {
      setSaveError(error instanceof Error ? error.message : labels.failed);
    } finally {
      setSaving(false);
    }
  }

  function openForm(model?: ModelConnection) {
    setSaveError("");
    setSaved(false);
    setDraft({
      existingName: model?.name,
      name: model?.name ?? "",
      displayName: model?.display_name ?? "",
      provider: model?.provider ?? "openai-compatible",
      modelId: model?.model ?? "",
      baseURL: model?.base_url ?? "",
      apiKey: "",
      makeDefault: !!model && settings.data?.default_model === model.name,
    });
  }

  return (
    <SettingsSection title={labels.title}>
      {settings.isPending && (
        <p className="text-muted-foreground text-sm">{t.common.loading}</p>
      )}
      {settings.isError && (
        <p role="alert" className="text-destructive text-sm">
          {settings.error.message}
        </p>
      )}
      {settings.data && (
        <div className="space-y-4">
          {!draft && writable && (
            <Button size="sm" onClick={() => openForm()}>
              <PlusIcon />
              {labels.add}
            </Button>
          )}
          {saved && (
            <p role="status" className="text-sm">
              {labels.saved}
            </p>
          )}
          {draft && (
            <form onSubmit={submit} className="space-y-4 rounded-md border p-4">
              <fieldset
                disabled={saving}
                className="grid min-w-0 gap-4 sm:grid-cols-2"
              >
                <label className="grid gap-1.5 text-sm font-medium">
                  {labels.name}
                  <Input
                    required
                    value={draft.name}
                    readOnly={!!draft.existingName}
                    onChange={(event) =>
                      setDraft({ ...draft, name: event.target.value })
                    }
                  />
                </label>
                <label className="grid gap-1.5 text-sm font-medium">
                  {labels.displayName}
                  <Input
                    value={draft.displayName}
                    onChange={(event) =>
                      setDraft({ ...draft, displayName: event.target.value })
                    }
                  />
                </label>
                <div className="grid gap-1.5 text-sm font-medium">
                  <label htmlFor={providerId}>{labels.provider}</label>
                  <select
                    id={providerId}
                    value={draft.provider}
                    className="border-input bg-background h-9 rounded-md border px-3 text-sm"
                    onChange={(event) =>
                      setDraft({
                        ...draft,
                        provider: event.target.value as ModelProvider,
                      })
                    }
                  >
                    {Object.entries(providers).map(([key, name]) => (
                      <option key={key} value={key}>
                        {name}
                      </option>
                    ))}
                  </select>
                </div>
                <label className="grid gap-1.5 text-sm font-medium">
                  {labels.modelId}
                  <Input
                    required
                    value={draft.modelId}
                    onChange={(event) =>
                      setDraft({ ...draft, modelId: event.target.value })
                    }
                  />
                </label>
                <label className="grid gap-1.5 text-sm font-medium">
                  {labels.baseUrl}
                  <Input
                    type="url"
                    value={draft.baseURL}
                    placeholder="https://api.example.com/v1"
                    onChange={(event) =>
                      setDraft({ ...draft, baseURL: event.target.value })
                    }
                  />
                </label>
                <label className="grid gap-1.5 text-sm font-medium">
                  {labels.apiKey}
                  <Input
                    type="password"
                    autoComplete="new-password"
                    value={draft.apiKey}
                    placeholder={
                      draft.existingName ? labels.keepApiKey : undefined
                    }
                    onChange={(event) =>
                      setDraft({ ...draft, apiKey: event.target.value })
                    }
                  />
                </label>
                <label className="flex items-center gap-2 text-sm sm:col-span-2">
                  <input
                    type="checkbox"
                    className="accent-primary"
                    checked={draft.makeDefault}
                    disabled={
                      settings.data.default_model === draft.existingName
                    }
                    onChange={(event) =>
                      setDraft({ ...draft, makeDefault: event.target.checked })
                    }
                  />
                  {labels.setDefault}
                </label>
              </fieldset>
              {saveError && (
                <p role="alert" className="text-destructive text-sm">
                  {labels.failed} {saveError}
                </p>
              )}
              <div className="flex gap-2">
                <Button size="sm" type="submit" disabled={saving}>
                  {t.common.save}
                </Button>
                <Button
                  size="sm"
                  type="button"
                  variant="outline"
                  disabled={saving}
                  onClick={() => setDraft(undefined)}
                >
                  {t.common.cancel}
                </Button>
              </div>
            </form>
          )}
          {settings.data.models.length === 0 ? (
            <p className="text-muted-foreground text-sm">{labels.empty}</p>
          ) : (
            <ul className="divide-y rounded-md border">
              {settings.data.models.map((model) => (
                <li
                  key={model.name}
                  className="flex items-center justify-between gap-3 p-3"
                >
                  <div className="min-w-0 space-y-1">
                    <div className="flex flex-wrap items-center gap-2 text-sm font-medium">
                      <span className="break-all">
                        {model.display_name || model.name}
                      </span>
                      {model.name === settings.data.default_model && (
                        <Badge variant="secondary">{labels.defaultBadge}</Badge>
                      )}
                    </div>
                    <p className="text-muted-foreground text-xs break-all">
                      {providers[model.provider]} · {model.model}
                    </p>
                  </div>
                  {writable && (
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={!!draft}
                      aria-label={`${labels.edit} ${model.display_name || model.name}`}
                      onClick={() => openForm(model)}
                    >
                      {labels.edit}
                    </Button>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </SettingsSection>
  );
}
