"use client";

import { CheckIcon, PencilIcon, XIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Streamdown } from "streamdown";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { useI18n } from "@/core/i18n/hooks";
import type { PendingUserQuestion } from "@/core/threads/types";

export function PlanReview({
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
  const [feedback, setFeedback] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const answer = async (payload: {
    selected?: string[];
    custom?: string;
    dismiss?: boolean;
  }) => {
    setSubmitting(true);
    try {
      await onAnswer(payload);
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t.planReview.submitError,
      );
      setSubmitting(false);
    }
  };

  return (
    <section
      className="mt-3 flex min-h-0 w-full flex-col overflow-hidden rounded-lg border border-[var(--manus-line-strong)] bg-white shadow-[0_16px_48px_rgba(38,38,34,0.1)]"
      style={{ maxHeight: "min(42rem, calc(100dvh - 12rem))" }}
    >
      <header className="flex shrink-0 items-start justify-between gap-4 border-b px-4 py-3">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold">{t.planReview.title}</h2>
          <p className="text-muted-foreground mt-0.5 text-xs">
            {t.planReview.description}
          </p>
          <p className="text-foreground/75 mt-1 text-xs font-medium">
            {t.planReview.onceHint}
          </p>
        </div>
        <Button
          aria-label={t.planReview.dismiss}
          title={t.planReview.dismiss}
          size="icon-sm"
          variant="ghost"
          disabled={submitting}
          onClick={() => void answer({ dismiss: true })}
        >
          <XIcon />
        </Button>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain border-b px-4 py-3">
        <Streamdown className="max-w-none text-sm leading-6 break-words [&_h1]:my-3 [&_h1]:text-xl [&_h1]:font-semibold [&_h2]:my-3 [&_h2]:text-lg [&_h2]:font-semibold [&_h3]:my-2 [&_h3]:text-base [&_h3]:font-semibold [&_li]:my-1 [&_ol]:my-2 [&_p]:my-2 [&_ul]:my-2">
          {question.detail ?? ""}
        </Streamdown>
      </div>
      <div className="shrink-0 space-y-3 bg-white p-3">
        <Textarea
          aria-label="Plan feedback"
          className="max-h-28 min-h-16 resize-y text-sm"
          disabled={submitting}
          placeholder={t.planReview.feedbackPlaceholder}
          value={feedback}
          onChange={(event) => setFeedback(event.target.value)}
        />
        <div className="flex flex-wrap justify-end gap-2">
          <Button
            variant="outline"
            disabled={submitting}
            onClick={() =>
              void answer({
                selected: ["Keep planning"],
                custom: feedback.trim() || undefined,
              })
            }
          >
            <PencilIcon />
            {t.planReview.keepPlanning}
          </Button>
          <Button
            disabled={submitting}
            onClick={() => void answer({ selected: ["Approve"] })}
          >
            <CheckIcon />
            {t.planReview.approveEntirePlan}
          </Button>
        </div>
      </div>
    </section>
  );
}
