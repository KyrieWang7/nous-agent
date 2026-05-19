import { cleanThreadTitle } from "@/core/threads/utils";

import { FlipDisplay } from "./flip-display";

export function ThreadTitle({
  threadTitle,
}: {
  className?: string;
  threadId: string;
  threadTitle: string;
}) {
  const displayTitle = cleanThreadTitle(threadTitle);
  return <FlipDisplay uniqueKey={displayTitle}>{displayTitle}</FlipDisplay>;
}
