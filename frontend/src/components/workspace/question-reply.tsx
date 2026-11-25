"use client";

import { useId, useRef, useState } from "react";
import { MessageCircleQuestionMarkIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { useI18n } from "@/core/i18n/hooks";
import type { PendingUserQuestion } from "@/core/threads/types";

export function ClarificationSummary({
  question,
  result,
}: {
  question?: string;
  result: string;
}) {
  const { t } = useI18n();
  const skipped = result.startsWith("The user skipped the question.");
  let answer = "";
  if (result.startsWith("User answer: ")) {
    try {
      const payload = JSON.parse(result.slice("User answer: ".length)) as {
        selected?: unknown;
        custom?: unknown;
      } | null;
      const selected = Array.isArray(payload?.selected)
        ? payload.selected.filter((value): value is string => typeof value === "string")
        : [];
      answer = [...selected, typeof payload?.custom === "string" ? payload.custom : ""]
        .filter(Boolean)
        .join(" · ");
    } catch {
      // Older or incomplete tool results can still display the question.
    }
  }
  return (
    <section aria-label={t.questionReply.summary} className="bg-muted/30 rounded-lg border px-3 py-2.5 text-sm">
      <div className="flex items-start gap-2">
        <MessageCircleQuestionMarkIcon className="text-muted-foreground mt-0.5 size-4 shrink-0" aria-hidden />
        <div className="min-w-0 break-words">
          <p className="font-medium">{question || t.questionReply.summary}</p>
          <p className="text-muted-foreground mt-0.5 whitespace-pre-wrap">
            {skipped ? t.questionReply.skipped : answer || t.questionReply.closed}
          </p>
        </div>
      </div>
    </section>
  );
}

export function QuestionReply({
  question,
  onAnswer,
}: {
  question: PendingUserQuestion;
  onAnswer: (answer: {
    selected?: string[];
    custom?: string;
    dismiss?: boolean;
  }) => Promise<void>;
}) {
  const { t } = useI18n();
  const id = useId();
  const [selected, setSelected] = useState("");
  const [custom, setCustom] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState("");
  const submitting = useRef(false);
  const answer = async (dismiss = false) => {
    if (submitting.current) return;
    submitting.current = true;
    setSending(true);
    setError("");
    try {
      await onAnswer(
        dismiss
          ? { dismiss: true }
          : {
              ...(selected ? { selected: [selected] } : {}),
              ...(custom.trim() ? { custom: custom.trim() } : {}),
            },
      );
    } catch (error) {
      setError(
        error instanceof Error ? error.message : t.planReview.submitError,
      );
      submitting.current = false;
      setSending(false);
    }
  };

  return (
    <form
      aria-labelledby={id}
      className="bg-card space-y-3 rounded-lg border p-4"
      onSubmit={(event) => {
        event.preventDefault();
        if (selected || custom.trim()) void answer();
      }}
    >
      <h2 id={id} className="font-medium">
        {question.header || question.question}
      </h2>
      {question.header !== question.question && (
        <p className="text-sm">{question.question}</p>
      )}
      {question.detail && (
        <p className="text-muted-foreground text-sm whitespace-pre-wrap">
          {question.detail}
        </p>
      )}
      <div className="max-h-[40dvh] space-y-3 overflow-y-auto">
        {!!question.options?.length && (
          <fieldset disabled={sending} className="space-y-2">
            <legend className="sr-only">{t.questionReply.options}</legend>
            {question.options.map((option) => (
              <label
                key={option.label}
                className="flex cursor-pointer items-start gap-2 rounded-md border px-3 py-2 text-sm"
              >
                <input
                  className="mt-1"
                  type="radio"
                  name={id}
                  value={option.label}
                  checked={selected === option.label}
                  onChange={() => setSelected(option.label)}
                />
                <span className="min-w-0 break-words">
                  {option.label}
                  {option.description && (
                    <span className="text-muted-foreground block">
                      {option.description}
                    </span>
                  )}
                </span>
              </label>
            ))}
          </fieldset>
        )}
        <Textarea
          aria-label={t.questionReply.answer}
          placeholder={t.questionReply.answer}
          value={custom}
          onChange={(event) => setCustom(event.target.value)}
          disabled={sending}
        />
      </div>
      {error && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        <Button
          type="button"
          variant="ghost"
          disabled={sending}
          onClick={() => void answer(true)}
        >
          {t.questionReply.skip}
        </Button>
        <Button
          type="submit"
          disabled={sending || (!selected && !custom.trim())}
        >
          {t.questionReply.submit}
        </Button>
      </div>
    </form>
  );
}
