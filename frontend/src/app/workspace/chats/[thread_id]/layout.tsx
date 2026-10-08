"use client";

import { useParams } from "next/navigation";

import { PromptInputProvider } from "@/components/ai-elements/prompt-input";
import { ArtifactsProvider } from "@/components/workspace/artifacts";
import { SubtasksProvider } from "@/core/tasks/context";

export default function ChatLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const { thread_id: threadId } = useParams<{ thread_id: string }>();
  return (
    <SubtasksProvider>
      <ArtifactsProvider>
        <PromptInputProvider draftKey={threadId}>
          {children}
        </PromptInputProvider>
      </ArtifactsProvider>
    </SubtasksProvider>
  );
}
