"use client";

import { FilesIcon, XIcon } from "lucide-react";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { ConversationEmptyState } from "@/components/ai-elements/conversation";
import { usePromptInputController } from "@/components/ai-elements/prompt-input";
import { Button } from "@/components/ui/button";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import { useSidebar } from "@/components/ui/sidebar";
import {
  ArtifactFileDetail,
  ArtifactFileList,
  useArtifacts,
} from "@/components/workspace/artifacts";
import { InputBox } from "@/components/workspace/input-box";
import { MessageList } from "@/components/workspace/messages";
import { ThreadContext } from "@/components/workspace/messages/context";
import { PlanReview } from "@/components/workspace/plan-review";
import { SubagentDraggablePanel } from "@/components/workspace/subagent";
import { SwarmDraggablePanel } from "@/components/workspace/swarm/swarm-panel";
import { ThreadTitle } from "@/components/workspace/thread-title";
import { TodoList } from "@/components/workspace/todo-list";
import { Tooltip } from "@/components/workspace/tooltip";
import { Welcome } from "@/components/workspace/welcome";
import { useI18n } from "@/core/i18n/hooks";
import { useNotification } from "@/core/notification/hooks";
import { useLocalSettings } from "@/core/settings";
import { getTeamsByThread } from "@/core/swarm/api";
import { useSubtaskContext } from "@/core/tasks/context";
import { useSubmitThread, useThreadStream } from "@/core/threads/hooks";
import {
  cleanThreadTitle,
  pathOfThread,
  textOfMessage,
} from "@/core/threads/utils";
import { uuid } from "@/core/utils/uuid";
import { env } from "@/env";
import { cn } from "@/lib/utils";

export default function ChatPage() {
  const { t } = useI18n();
  const router = useRouter();
  const [settings, setSettings] = useLocalSettings();
  const { setOpen: setSidebarOpen } = useSidebar();
  const {
    artifacts,
    open: artifactsOpen,
    setOpen: setArtifactsOpen,
    setArtifacts,
    select: selectArtifact,
    selectedArtifact,
  } = useArtifacts();
  const { thread_id: threadIdFromPath } = useParams<{ thread_id: string }>();
  const searchParams = useSearchParams();
  const promptInputController = usePromptInputController();
  const inputInitialValue = useMemo(() => {
    if (threadIdFromPath !== "new" || searchParams.get("mode") !== "skill") {
      return undefined;
    }
    return t.inputBox.createSkillPrompt;
  }, [threadIdFromPath, searchParams, t.inputBox.createSkillPrompt]);
  const lastInitialValueRef = useRef<string | undefined>(undefined);
  const setInputRef = useRef(promptInputController.textInput.setInput);
  setInputRef.current = promptInputController.textInput.setInput;
  useEffect(() => {
    if (
      inputInitialValue &&
      inputInitialValue !== lastInitialValueRef.current
    ) {
      lastInitialValueRef.current = inputInitialValue;
      setTimeout(() => {
        setInputRef.current(inputInitialValue);
        const textarea = document.querySelector("textarea");
        if (textarea) {
          textarea.focus();
          textarea.selectionStart = textarea.value.length;
          textarea.selectionEnd = textarea.value.length;
        }
      }, 100);
    }
  }, [inputInitialValue]);
  const isNewThreadFromPath = threadIdFromPath === "new";
  const [hasSubmitted, setHasSubmitted] = useState(false);
  const isNewThread = isNewThreadFromPath && !hasSubmitted;
  const [threadId, setThreadId] = useState<string | null>(null);
  useEffect(() => {
    if (threadIdFromPath !== "new") {
      setThreadId(threadIdFromPath);
      setHasSubmitted(false);
    } else {
      setThreadId(uuid());
      setHasSubmitted(false);
    }
    setSubagentPanelDismissed(false);
  }, [threadIdFromPath]);

  const { showNotification } = useNotification();
  const [isStreaming, setIsStreaming] = useState(false);
  const thread = useThreadStream({
    isNewThread,
    threadId,
    onFinish: (state) => {
      setIsStreaming(false);
      if (document.hidden || !document.hasFocus()) {
        let body = "Conversation finished";
        const lastMessage = state.messages[state.messages.length - 1];
        if (lastMessage) {
          const textContent = textOfMessage(lastMessage);
          if (textContent) {
            if (textContent.length > 200) {
              body = textContent.substring(0, 200) + "...";
            } else {
              body = textContent;
            }
          }
        }
        showNotification(state.title, {
          body,
        });
      }
    },
  });
  useEffect(() => {
    if (thread.threadNotFound && threadIdFromPath !== "new") {
      router.replace("/workspace/chats/new");
    }
  }, [router, thread.threadNotFound, threadIdFromPath]);
  const title = useMemo(() => {
    let result = isNewThread
      ? ""
      : cleanThreadTitle(thread.values?.title ?? "Untitled");
    if (result === "Untitled") {
      result = "";
    }
    return result;
  }, [thread, isNewThread]);

  useEffect(() => {
    const cleanedTitle = cleanThreadTitle(thread.values?.title);
    const pageTitle = isNewThread
      ? t.pages.newChat
      : cleanedTitle !== "Untitled"
        ? cleanedTitle
        : t.pages.untitled;
    if (thread.isThreadLoading) {
      document.title = `Loading... - ${t.pages.appName}`;
    } else {
      document.title = `${pageTitle} - ${t.pages.appName}`;
    }
  }, [
    isNewThread,
    t.pages.newChat,
    t.pages.untitled,
    t.pages.appName,
    thread.values.title,
    thread.isThreadLoading,
  ]);

  const [autoSelectFirstArtifact, setAutoSelectFirstArtifact] = useState(true);
  useEffect(() => {
    setArtifacts(thread.values.artifacts);
    if (
      env.NEXT_PUBLIC_STATIC_WEBSITE_ONLY === "true" &&
      autoSelectFirstArtifact
    ) {
      if (thread?.values?.artifacts?.length > 0) {
        setAutoSelectFirstArtifact(false);
        selectArtifact(thread.values.artifacts[0]!);
      }
    }
  }, [
    autoSelectFirstArtifact,
    selectArtifact,
    setArtifacts,
    thread.values.artifacts,
  ]);

  const artifactPanelOpen = useMemo(() => {
    if (env.NEXT_PUBLIC_STATIC_WEBSITE_ONLY === "true") {
      return artifactsOpen && artifacts?.length > 0;
    }
    return artifactsOpen;
  }, [artifactsOpen, artifacts]);

  const lastArtifactPanelOpenRef = useRef(artifactPanelOpen);
  useEffect(() => {
    if (lastArtifactPanelOpenRef.current && !artifactPanelOpen) {
      setSidebarOpen(true);
    }
    lastArtifactPanelOpenRef.current = artifactPanelOpen;
  }, [artifactPanelOpen, setSidebarOpen]);

  const [todoListCollapsed, setTodoListCollapsed] = useState(true);
  const [swarmTeamId, setSwarmTeamId] = useState<string | null>(null);
  const swarmDismissedRef = useRef(false);

  const swarmEnabled = Boolean(settings.context.swarm_enabled);
  const swarmPanelOpen = swarmEnabled && !!swarmTeamId;

  // Subagent panel state
  const { tasks: subtasks } = useSubtaskContext();
  const subagentEnabled = settings.context.mode === "ultra" || swarmEnabled;
  const hasSubtasks = Object.keys(subtasks).length > 0;
  const [subagentPanelDismissed, setSubagentPanelDismissed] = useState(false);
  const subagentPanelOpen =
    subagentEnabled &&
    hasSubtasks &&
    !swarmPanelOpen &&
    !subagentPanelDismissed;

  useEffect(() => {
    if (!swarmEnabled || !threadId || swarmDismissedRef.current) return;

    let cancelled = false;

    const pollForTeam = () => {
      getTeamsByThread(threadId)
        .then((teams) => {
          if (!cancelled && teams.length > 0) {
            setSwarmTeamId(teams[0]!.id);
          }
        })
        .catch(() => undefined);
    };

    pollForTeam();
    const interval = setInterval(pollForTeam, 3000);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [swarmEnabled, threadId]);

  const _handleSubmit = useSubmitThread({
    isNewThread,
    threadId,
    thread,
    threadContext: {
      ...settings.context,
    },
    afterSubmit() {
      if (isNewThreadFromPath && threadId) {
        setHasSubmitted(true);
        window.history.replaceState(null, "", pathOfThread(threadId));
      }
    },
  });
  const handleSubmit = useCallback(
    async (message: Parameters<typeof _handleSubmit>[0]) => {
      setIsStreaming(true);
      await _handleSubmit(message);
    },
    [_handleSubmit],
  );
  const handleStop = useCallback(() => {
    setIsStreaming(false);
    thread.stop();
  }, [thread]);

  if (!threadId) {
    return null;
  }

  return (
    <ThreadContext.Provider value={{ threadId, thread }}>
      <ResizablePanelGroup orientation="horizontal" className="min-h-0">
        <ResizablePanel
          className="relative overflow-hidden"
          defaultSize={artifactPanelOpen ? 46 : 100}
          minSize={artifactPanelOpen ? 30 : 100}
        >
          <div className="flex h-full w-full overflow-hidden">
            <div className="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
              <header
                className={cn(
                  "absolute top-0 right-0 left-0 z-30 flex h-12 shrink-0 items-center px-4",
                  isNewThread
                    ? "bg-background/0 backdrop-blur-none"
                    : "bg-background/82 shadow-[0_1px_0_rgba(38,38,34,0.06)] backdrop-blur",
                )}
              >
                <div className="flex w-full items-center text-sm font-medium">
                  {title !== "Untitled" && (
                    <ThreadTitle threadId={threadId} threadTitle={title} />
                  )}
                </div>
                <div>
                  {artifacts?.length > 0 && !artifactsOpen && (
                    <Tooltip content="Show artifacts of this conversation">
                      <Button
                        className="text-muted-foreground hover:text-foreground"
                        variant="ghost"
                        onClick={() => {
                          setArtifactsOpen(true);
                          setSidebarOpen(false);
                        }}
                      >
                        <FilesIcon />
                        {t.common.artifacts}
                      </Button>
                    </Tooltip>
                  )}
                </div>
              </header>
              <main className="flex min-h-0 max-w-full flex-1 flex-col">
                <div className="flex min-h-0 flex-1 justify-center">
                  <MessageList
                    className={cn("size-full", !isNewThread && "pt-10")}
                    threadId={threadId}
                    thread={thread}
                    paddingBottom={
                      isNewThread ? (todoListCollapsed ? 160 : 280) : 24
                    }
                  />
                </div>
                <div
                  className={cn(
                    "right-0 bottom-0 left-0 z-30 flex justify-center px-4",
                    isNewThread ? "absolute pb-4" : "relative shrink-0 pb-4",
                  )}
                >
                  <div
                    className={cn(
                      "relative w-full",
                      isNewThread && "-translate-y-[calc(50vh-96px)]",
                      isNewThread
                        ? "max-w-(--container-width-sm)"
                        : "max-w-(--container-width-md)",
                    )}
                  >
                    <div
                      className={cn(
                        "right-0 left-0 z-0",
                        isNewThread ? "absolute -top-4" : "relative",
                      )}
                    >
                      <div
                        className={cn(
                          "right-0 bottom-0 left-0",
                          isNewThread ? "absolute" : "relative",
                        )}
                      >
                        <TodoList
                          className="bg-white/80"
                          todos={thread.values.todos ?? []}
                          collapsed={todoListCollapsed}
                          hidden={
                            !thread.values.todos ||
                            thread.values.todos.length === 0
                          }
                          onToggle={() =>
                            setTodoListCollapsed(!todoListCollapsed)
                          }
                        />
                      </div>
                    </div>
                    {thread.pendingQuestion?.intent === "plan-review" ? (
                      <PlanReview
                        question={thread.pendingQuestion}
                        onAnswer={thread.answerQuestion}
                      />
                    ) : (
                      <InputBox
                        className={cn("w-full")}
                        isNewThread={isNewThread}
                        autoFocus={isNewThread}
                        status={isStreaming ? "streaming" : "ready"}
                        context={settings.context}
                        extraHeader={
                          isNewThread && <Welcome mode={settings.context.mode} />
                        }
                        disabled={env.NEXT_PUBLIC_STATIC_WEBSITE_ONLY === "true"}
                        onContextChange={(context) =>
                          setSettings("context", context)
                        }
                        tokenUsage={thread.tokenUsage}
                        onSubmit={handleSubmit}
                        onStop={handleStop}
                      />
                    )}
                    {env.NEXT_PUBLIC_STATIC_WEBSITE_ONLY === "true" && (
                      <div className="text-muted-foreground/67 w-full translate-y-12 text-center text-xs">
                        {t.common.notAvailableInDemoMode}
                      </div>
                    )}
                  </div>
                </div>
              </main>
            </div>
            {swarmPanelOpen && (
              <SwarmDraggablePanel
                teamId={swarmTeamId}
                onClose={() => {
                  setSwarmTeamId(null);
                  swarmDismissedRef.current = true;
                }}
              />
            )}
            {subagentPanelOpen && (
              <SubagentDraggablePanel
                onClose={() => setSubagentPanelDismissed(true)}
                tokenUsage={thread.tokenUsage}
              />
            )}
          </div>
        </ResizablePanel>
        <ResizableHandle
          className={cn(
            "opacity-33 hover:opacity-100",
            !artifactPanelOpen && "pointer-events-none opacity-0",
          )}
        />
        <ResizablePanel
          className={cn(
            "transition-all duration-300 ease-in-out",
            !artifactsOpen && "opacity-0",
          )}
          defaultSize={artifactPanelOpen ? 64 : 0}
          minSize={0}
          maxSize={artifactPanelOpen ? undefined : 0}
        >
          <div
            className={cn(
              "h-full p-4 transition-transform duration-300 ease-in-out",
              artifactPanelOpen ? "translate-x-0" : "translate-x-full",
            )}
          >
            {selectedArtifact ? (
              <ArtifactFileDetail
                className="size-full"
                filepath={selectedArtifact}
                threadId={threadId}
              />
            ) : (
              <div className="relative flex size-full justify-center">
                <div className="absolute top-1 right-1 z-30">
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    onClick={() => {
                      setArtifactsOpen(false);
                      setSidebarOpen(true);
                    }}
                  >
                    <XIcon />
                  </Button>
                </div>
                {thread.values.artifacts?.length === 0 ? (
                  <ConversationEmptyState
                    icon={<FilesIcon />}
                    title="No artifact selected"
                    description="Select an artifact to view its details"
                  />
                ) : (
                  <div className="flex size-full max-w-(--container-width-sm) flex-col justify-center p-4 pt-8">
                    <header className="shrink-0">
                      <h2 className="text-lg font-medium">Artifacts</h2>
                    </header>
                    <main className="min-h-0 grow">
                      <ArtifactFileList
                        className="max-w-(--container-width-sm) p-4 pt-12"
                        files={thread.values.artifacts ?? []}
                        threadId={threadId}
                      />
                    </main>
                  </div>
                )}
              </div>
            )}
          </div>
        </ResizablePanel>
      </ResizablePanelGroup>
    </ThreadContext.Provider>
  );
}
