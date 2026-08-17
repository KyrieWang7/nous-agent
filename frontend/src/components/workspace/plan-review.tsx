"use client";

import { CheckIcon, PencilIcon, XIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Streamdown } from "streamdown";

import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Textarea } from "@/components/ui/textarea";
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
      toast.error(error instanceof Error ? error.message : "Could not submit plan review");
      setSubmitting(false);
    }
  };

  return (
    <section className="w-full overflow-hidden rounded-lg border border-[var(--manus-line-strong)] bg-white shadow-[0_16px_48px_rgba(38,38,34,0.1)]">
      <header className="flex items-start justify-between gap-4 border-b px-4 py-3">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold">{question.header}</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">{question.question}</p>
        </div>
        <Button
          aria-label="Dismiss plan review"
          title="Dismiss"
          size="icon-sm"
          variant="ghost"
          disabled={submitting}
          onClick={() => void answer({ dismiss: true })}
        >
          <XIcon />
        </Button>
      </header>
      <ScrollArea className="max-h-72 border-b px-4 py-3">
        <Streamdown className="text-sm">{question.detail ?? ""}</Streamdown>
      </ScrollArea>
      <div className="space-y-3 p-3">
        <Textarea
          aria-label="Plan feedback"
          className="min-h-18 resize-none"
          disabled={submitting}
          placeholder="Feedback for another planning pass (optional)"
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
            Keep planning
          </Button>
          <Button
            disabled={submitting}
            onClick={() => void answer({ selected: ["Approve"] })}
          >
            <CheckIcon />
            Approve plan
          </Button>
        </div>
      </div>
    </section>
  );
}
