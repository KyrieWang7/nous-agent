"use client";

import {
  FilesIcon,
  ListTreeIcon,
  ClipboardListIcon,
  TriangleAlertIcon,
  UsersIcon,
} from "lucide-react";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";

import { usePromptInputController } from "@/components/ai-elements/prompt-input";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { SidebarTrigger, useSidebar } from "@/components/ui/sidebar";
import { useArtifacts } from "@/components/workspace/artifacts";
import { WorkspaceFilePanel } from "@/components/workspace/artifacts/workspace-file-panel";
import { InputBox, SuggestionList } from "@/components/workspace/input-box";
import { MessageList } from "@/components/workspace/messages";
import { ThreadContext } from "@/components/workspace/messages/context";
import { PlanReview } from "@/components/workspace/plan-review";
import { QuestionReply } from "@/components/workspace/question-reply";
import { RunEventInspector } from "@/components/workspace/run-event-inspector";
import {
  SessionInbox,
  useSessionInbox,
} from "@/components/workspace/session-inbox";
import { SubagentPanel } from "@/components/workspace/subagent/subagent-panel";
import { SwarmPanel } from "@/components/workspace/swarm/swarm-panel";
import { ThreadTitle } from "@/components/workspace/thread-title";
import { TodoList } from "@/components/workspace/todo-list";
import { Welcome } from "@/components/workspace/welcome";
import {
  WorkspaceWorkbench,
  type WorkbenchTab,
} from "@/components/workspace/workspace-workbench";
import { useI18n } from "@/core/i18n/hooks";
import { useNotification } from "@/core/notification/hooks";
import { useLocalSettings } from "@/core/settings";
import { continueChild } from "@/core/subagents/api";
import { ChildSessionsContext } from "@/core/subagents/context";
import { useChildCatalog } from "@/core/subagents/hooks";
import { getTeamsByThread } from "@/core/swarm/api";
import { useSubtaskContext } from "@/core/tasks/context";
import {
  browserDraftStorage,
  readDraft,
  writeDraft,
} from "@/core/threads/drafts";
import { useSubmitThread, useThreadStream } from "@/core/threads/hooks";
import type { InputDelivery } from "@/core/threads/inbox";
import { fetchActiveRunId, fetchLatestRunId } from "@/core/threads/transport";
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
  const { isMobile } = useSidebar();
  const {
    open: artifactsOpen,
    setOpen: setArtifactsOpen,
    setArtifacts,
    select: selectArtifact,
  } = useArtifacts();
  const { thread_id: threadIdFromPath } = useParams<{ thread_id: string }>();
  const searchParams = useSearchParams();
  const promptInputController = usePromptInputController();
  const currentInputRef = useRef({
    path: threadIdFromPath,
    controller: promptInputController,
  });
  currentInputRef.current = {
    path: threadIdFromPath,
    controller: promptInputController,
  };
  const inputInitialValue = useMemo(() => {
    if (threadIdFromPath !== "new" || searchParams.get("mode") !== "skill") {
      return undefined;
    }
    return t.inputBox.createSkillPrompt;
  }, [threadIdFromPath, searchParams, t.inputBox.createSkillPrompt]);
  const initializedPromptRef = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (initializedPromptRef.current === inputInitialValue) return;
    initializedPromptRef.current = inputInitialValue;
    if (inputInitialValue && !promptInputController.textInput.value) {
      promptInputController.textInput.setInput(inputInitialValue);
    }
  }, [inputInitialValue, promptInputController.textInput]);
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
  const submissionPending = useRef(false);
  const thread = useThreadStream({
    isNewThread,
    threadId,
    onFinish: (state) => {
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

  const [todoListCollapsed, setTodoListCollapsed] = useState(true);
  const [swarmTeamId, setSwarmTeamId] = useState<string | null>(null);
  const [swarmDismissed, setSwarmDismissed] = useState(false);

  const swarmPanelOpen = !!swarmTeamId && !swarmDismissed;

  // Subagent panel state
  const { tasks: subtasks } = useSubtaskContext();

  const hasSubtasks = thread.messages.some(
    (message) =>
      message.type === "ai" &&
      message.tool_calls?.some((call) => !!call.id && !!subtasks[call.id]),
  );
  const [subagentPanelDismissed, setSubagentPanelDismissed] = useState(false);
  const catalog = useChildCatalog(
    threadId,
    !isNewThread && env.NEXT_PUBLIC_STATIC_WEBSITE_ONLY !== "true",
    thread.isLoading,
  );
  const sessions = catalog.data ?? [];
  const selectedSessionId = searchParams.get("subagent");
  const [executionTab, setExecutionTab] = useState<"agents" | "plan">("agents");
  const [panelRequested, setPanelRequested] = useState(false);
  const panelOpener = useRef<HTMLElement | null>(null);
  const [childCommandPending, setChildCommandPending] = useState(false);
  const subagentPanelOpen =
    !isNewThread &&
    (panelRequested ||
      !!selectedSessionId ||
      ((hasSubtasks || sessions.length > 0) &&
        !subagentPanelDismissed &&
        !swarmPanelOpen));
  const selectSession = useCallback(
    (id: string) => {
      const url = new URL(window.location.href);
      url.searchParams.set("subagent", id);
      window.history.replaceState(null, "", url);
      setExecutionTab("agents");
      setPanelRequested(true);
      setSubagentPanelDismissed(false);
      setArtifactsOpen(false);
    },
    [setArtifactsOpen],
  );
  const closeExecutionPanel = useCallback(() => {
    setArtifactsOpen(false);
    setSwarmDismissed(true);
    const url = new URL(window.location.href);
    url.searchParams.delete("subagent");
    window.history.replaceState(null, "", url);
    setPanelRequested(false);
    setSubagentPanelDismissed(true);
    requestAnimationFrame(() => panelOpener.current?.focus());
  }, [setArtifactsOpen]);
  const openWorkbench = (tab: WorkbenchTab) => {
    if (tab === "files" || tab === "swarm") {
      const url = new URL(window.location.href);
      url.searchParams.delete("subagent");
      window.history.replaceState(null, "", url);
      setPanelRequested(false);
      setSubagentPanelDismissed(true);
      setSwarmDismissed(tab !== "swarm");
      setArtifactsOpen(tab === "files");
      return;
    }
    setExecutionTab(tab);
    setPanelRequested(true);
    setSubagentPanelDismissed(false);
    setSwarmDismissed(true);
    setArtifactsOpen(false);
  };
  const workbenchTab = artifactsOpen
    ? "files"
    : subagentPanelOpen
      ? executionTab
      : swarmPanelOpen
        ? "swarm"
        : null;
  const continueSession = useCallback(
    async (id: string, prompt: string) => {
      if (!threadId) return;
      setChildCommandPending(true);
      try {
        const run = await continueChild(threadId, id, prompt);
        // Server acceptance creates the run. Attach the existing stream consumer
        // without submitting a second prompt or creating another run.
        if (currentInputRef.current.path === threadId)
          void thread.attachRun(run.run_id);
      } catch (error) {
        // A lost acceptance response must never cause a second POST. Discover
        // the accepted server run and reconnect to it if it is still active.
        try {
          const active = await fetchActiveRunId(threadId);
          if (active && currentInputRef.current.path === threadId)
            void thread.attachRun(active);
        } catch {
          /* keep the original command error */
        }
        throw error;
      } finally {
        setChildCommandPending(false);
      }
    },
    [threadId, thread],
  );
  useEffect(() => {
    setPanelRequested(false);
    setExecutionTab("agents");
  }, [threadIdFromPath]);

  useEffect(() => {
    if (!threadId || isNewThread) return;

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
  }, [isNewThread, threadId]);

  useEffect(() => {
    setSwarmDismissed(false);
    setSwarmTeamId(null);
  }, [threadId]);
  useEffect(() => {
    if (artifactsOpen) {
      setSwarmDismissed(true);
      setSubagentPanelDismissed(true);
      setPanelRequested(false);
    }
  }, [artifactsOpen]);
  const inbox = useSessionInbox(threadId, !isNewThread);
  const running = !isNewThread && thread.isLoading && Boolean(thread.runId);
  const canSteer = running && !thread.isStopping && !thread.pendingQuestion;
  const latestInboxRun = [...(inbox.data ?? [])]
    .filter((e) => e.run_id)
    .sort(
      (a, b) =>
        (Date.parse(b.run_created_at ?? "") || 0) -
        (Date.parse(a.run_created_at ?? "") || 0),
    )[0];
  const lastInboxAttachment = useRef("");
  const seenInboxRuns = useRef(new Set<string>());
  useEffect(() => {
    const rid = latestInboxRun?.run_id;
    if (!rid || !threadId || thread.isLoading) return;
    const key = `${threadId}:${rid}`;
    const active =
      latestInboxRun?.run_status === "pending" ||
      latestInboxRun?.run_status === "running";
    if (thread.runId === rid && !thread.error) {
      seenInboxRuns.current.add(key);
      return;
    }
    if (seenInboxRuns.current.has(key) && (!thread.error || !active)) return;
    const attempt = `${key}:${inbox.dataUpdatedAt}`;
    if (lastInboxAttachment.current === attempt) return;
    lastInboxAttachment.current = attempt;
    let cancelled = false;
    void fetchLatestRunId(threadId)
      .then((latest) => {
        if (cancelled) return;
        seenInboxRuns.current.add(key);
        if (latest === rid) void thread.attachRun(rid);
      })
      .catch(() => {
        /* The next inbox poll retries this read. */
      });
    return () => {
      cancelled = true;
    };
  }, [
    latestInboxRun?.run_id,
    latestInboxRun?.run_status,
    thread,
    inbox.dataUpdatedAt,
    threadId,
  ]);
  const _handleSubmit = useSubmitThread({
    submissionDraft: {
      text: promptInputController.textInput.value,
      attachmentIds: promptInputController.attachments.files.map((f) => f.id),
    },
    isNewThread,
    threadId,
    thread,
    threadContext: {
      model_name: settings.context.model_name,
      mode: settings.context.mode,
      swarm_enabled: settings.context.swarm_enabled,
    },
    afterSubmit() {
      if (isNewThreadFromPath && threadId) {
        writeDraft(
          browserDraftStorage(),
          threadId,
          promptInputController.textInput.value,
        );
        setHasSubmitted(true);
        window.history.replaceState(null, "", pathOfThread(threadId));
      }
    },
  });
  const handleSubmit = useCallback(
    async (
      message: Parameters<typeof _handleSubmit>[0],
      delivery: InputDelivery,
    ) => {
      if (submissionPending.current)
        throw new Error("A message is already being submitted.");
      submissionPending.current = true;
      const submittedText = promptInputController.textInput.value;
      setIsStreaming(true);
      try {
        await _handleSubmit(message, canSteer ? delivery : "next_turn");
        if (
          threadId &&
          readDraft(browserDraftStorage(), threadId) === submittedText
        ) {
          writeDraft(browserDraftStorage(), threadId, "");
        }
        if (currentInputRef.current.path === threadId) {
          currentInputRef.current.controller.textInput.clear(submittedText);
        }
      } catch (error) {
        toast.error(
          error instanceof Error ? error.message : "Could not send message",
        );
        throw error;
      } finally {
        submissionPending.current = false;
        setIsStreaming(false);
      }
    },
    [_handleSubmit, canSteer, promptInputController.textInput.value, threadId],
  );
  const handleStop = useCallback(() => {
    thread.stop();
  }, [thread]);

  const lastStopKey = useRef<{
    threadId: string;
    runId: string;
    at: number;
  } | null>(null);

  if (!threadId) {
    return null;
  }

  return (
    <ChildSessionsContext.Provider
      value={{ sessions, openSession: selectSession }}
    >
      <ThreadContext.Provider value={{ threadId, thread }}>
        <div className="flex h-full min-h-0 w-full overflow-hidden">
          <div className="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
            <header
              className={cn(
                "absolute top-0 right-0 left-0 z-30 flex h-12 shrink-0 items-center px-4",
                isNewThread
                  ? "bg-background/0 backdrop-blur-none"
                  : "bg-background/82 shadow-[0_1px_0_rgba(38,38,34,0.06)] backdrop-blur",
              )}
            >
              <div className="flex w-full min-w-0 items-center text-sm font-medium">
                <SidebarTrigger
                  data-workspace-navigation
                  className="mr-2 md:hidden"
                />
                {!isNewThread && (
                  <ThreadTitle threadId={threadId} threadTitle={title} />
                )}
              </div>
              <div className="flex shrink-0 items-center gap-1">
                {!isNewThread &&
                  env.NEXT_PUBLIC_STATIC_WEBSITE_ONLY !== "true" && (
                    <RunEventInspector key={threadId} threadId={threadId} />
                  )}
                {!isNewThread &&
                  env.NEXT_PUBLIC_STATIC_WEBSITE_ONLY !== "true" && (
                    <>
                      <Button
                        data-agent-panel-trigger
                        aria-label={t.subagentWorkspace.title}
                        variant="ghost"
                        size="sm"
                        aria-expanded={
                          subagentPanelOpen && executionTab === "agents"
                        }
                        onClick={(event) => {
                          panelOpener.current = event.currentTarget;
                          openWorkbench("agents");
                        }}
                      >
                        <ListTreeIcon className="size-4" />
                        <span className="hidden lg:inline">
                          {t.subagentWorkspace.title}
                        </span>
                        {sessions.length > 0 && (
                          <span className="font-mono text-xs">
                            {sessions.length}
                          </span>
                        )}
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={t.subagentWorkspace.plan}
                        aria-expanded={
                          subagentPanelOpen && executionTab === "plan"
                        }
                        onClick={(event) => {
                          panelOpener.current = event.currentTarget;
                          openWorkbench("plan");
                        }}
                      >
                        <ClipboardListIcon className="size-4" />
                      </Button>
                    </>
                  )}
                {swarmTeamId && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t.workbenchPanel.swarm}
                    onClick={(event) => {
                      panelOpener.current = event.currentTarget;
                      openWorkbench("swarm");
                    }}
                  >
                    <UsersIcon className="size-4" />
                  </Button>
                )}
                {!isNewThread && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t.workbenchPanel.files}
                    aria-expanded={workbenchTab === "files"}
                    onClick={(event) => {
                      panelOpener.current = event.currentTarget;
                      openWorkbench("files");
                    }}
                  >
                    <FilesIcon className="size-4" />
                  </Button>
                )}
              </div>
            </header>
            <main
              className={cn(
                "flex min-h-0 max-w-full flex-1 flex-col",
                isNewThread && "overflow-y-auto pt-14 pb-6 sm:pt-16 sm:pb-10",
              )}
              onKeyDown={(event) => {
                if (
                  event.key !== "Escape" ||
                  event.defaultPrevented ||
                  event.repeat ||
                  event.nativeEvent.isComposing ||
                  event.nativeEvent.keyCode === 229 ||
                  event.altKey ||
                  event.ctrlKey ||
                  event.metaKey ||
                  event.shiftKey ||
                  !thread.isLoading ||
                  thread.isStopping ||
                  thread.pendingQuestion ||
                  !thread.runId ||
                  !(event.target instanceof HTMLElement) ||
                  event.target.closest(
                    "button, select, [role=dialog], [role=menu], [role=listbox], [aria-expanded=true], iframe",
                  )
                ) {
                  lastStopKey.current = null;
                  return;
                }
                const previous = lastStopKey.current;
                const at = performance.now();
                event.preventDefault();
                if (
                  previous?.threadId === threadId &&
                  previous.runId === thread.runId &&
                  at - previous.at <= 500
                ) {
                  lastStopKey.current = null;
                  handleStop();
                } else {
                  lastStopKey.current = {
                    threadId,
                    runId: thread.runId,
                    at,
                  };
                }
              }}
            >
              {!isNewThread && (
                <div className="flex min-h-0 flex-1 justify-center">
                  <MessageList
                    className="size-full pt-10"
                    threadId={threadId}
                    thread={thread}
                    paddingBottom={24}
                  />
                </div>
              )}
              <div
                className={cn(
                  "relative z-10 flex shrink-0 justify-center px-4",
                  isNewThread ? "my-auto" : "pb-4",
                )}
              >
                <div
                  className={cn(
                    "relative w-full",
                    isNewThread
                      ? "max-w-(--container-width-sm)"
                      : "max-w-(--container-width-md)",
                  )}
                >
                  {isNewThread && (
                    <Welcome
                      className="mb-5 sm:mb-7"
                      mode={settings.context.mode}
                    />
                  )}
                  {!!thread.values.todos?.length && (
                    <TodoList
                      className="bg-white/80"
                      todos={thread.values.todos}
                      collapsed={todoListCollapsed}
                      onToggle={() => setTodoListCollapsed(!todoListCollapsed)}
                    />
                  )}
                  {thread.isLoading && !thread.isStopping && (
                    <p
                      className="text-muted-foreground mb-1 px-2 text-[11px]"
                      role="status"
                    >
                      {t.subagentWorkspace.mainBusy}
                    </p>
                  )}
                  {thread.isStopping && (
                    <p
                      role="status"
                      className="text-muted-foreground mb-2 px-2 text-xs"
                    >
                      {t.subagentWorkspace.stoppingMain}
                    </p>
                  )}
                  {thread.error != null && (
                    <Alert
                      variant="destructive"
                      className="bg-background/95 mb-2 rounded-md"
                    >
                      <TriangleAlertIcon />
                      <AlertDescription>
                        {thread.error instanceof Error
                          ? thread.error.message
                          : "The run stopped before completion."}
                      </AlertDescription>
                    </Alert>
                  )}
                  {threadId && (
                    <SessionInbox
                      key={threadId}
                      threadId={threadId}
                      activeRunId={thread.isLoading ? thread.runId : null}
                      entries={inbox.data ?? []}
                      error={inbox.error}
                      onRefresh={() => {
                        void inbox.refetch();
                      }}
                      onRecovered={(draft, attachmentIds) => {
                        for (const id of attachmentIds ?? [])
                          promptInputController.attachments.remove(id);
                        if (promptInputController.textInput.value === draft) {
                          promptInputController.textInput.clear(draft);
                          writeDraft(browserDraftStorage(), threadId, "");
                        }
                        if (isNewThreadFromPath) {
                          setHasSubmitted(true);
                          window.history.replaceState(
                            null,
                            "",
                            pathOfThread(threadId),
                          );
                        }
                      }}
                    />
                  )}
                  {running &&
                    !thread.pendingQuestion &&
                    thread.values.is_plan_mode === true && (
                      <button
                        type="button"
                        className="bg-muted/40 text-muted-foreground hover:bg-muted mb-2 flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs"
                        onClick={() => {
                          setExecutionTab("plan");
                          setPanelRequested(true);
                          setSubagentPanelDismissed(false);
                          setArtifactsOpen(false);
                        }}
                      >
                        <ClipboardListIcon className="size-3" />
                        {t.subagentWorkspace.planMode}
                      </button>
                    )}
                  {thread.pendingQuestion ? (
                    <div className="space-y-2">
                      {thread.pendingQuestion.intent === "plan-review" ? (
                        <PlanReview
                          key={thread.pendingQuestion.id}
                          question={thread.pendingQuestion}
                          onAnswer={thread.answerQuestion}
                        />
                      ) : (
                        <QuestionReply
                          key={thread.pendingQuestion.id}
                          question={thread.pendingQuestion}
                          onAnswer={thread.answerQuestion}
                        />
                      )}
                      <Button
                        type="button"
                        variant="outline"
                        disabled={thread.isStopping}
                        onClick={handleStop}
                      >
                        {thread.isStopping
                          ? t.subagentWorkspace.stoppingMain
                          : t.subagentWorkspace.mainStop}
                      </Button>
                    </div>
                  ) : (
                    <InputBox
                      autoFocus={isNewThread}
                      running={running}
                      canSteer={canSteer}
                      stopping={thread.isStopping}
                      submitting={isStreaming}
                      context={settings.context}
                      disabled={
                        env.NEXT_PUBLIC_STATIC_WEBSITE_ONLY === "true" ||
                        childCommandPending ||
                        isStreaming
                      }
                      onContextChange={(context) =>
                        setSettings("context", context)
                      }
                      tokenUsage={thread.tokenUsage}
                      onSubmit={handleSubmit}
                      onStop={handleStop}
                    />
                  )}
                  {isNewThread &&
                    !thread.pendingQuestion &&
                    searchParams.get("mode") !== "skill" && (
                      <div className="mt-4 sm:mt-6">
                        <SuggestionList />
                      </div>
                    )}
                  {env.NEXT_PUBLIC_STATIC_WEBSITE_ONLY === "true" && (
                    <div className="text-muted-foreground/67 mt-3 w-full text-center text-xs">
                      {t.common.notAvailableInDemoMode}
                    </div>
                  )}
                </div>
              </div>
            </main>
          </div>
          {workbenchTab && (
            <WorkspaceWorkbench
              key={threadId}
              tab={workbenchTab}
              mobile={isMobile}
              hasSwarm={!!swarmTeamId}
              restoreFocus={() => panelOpener.current}
              onClose={closeExecutionPanel}
              onTab={openWorkbench}
            >
              {workbenchTab === "swarm" && swarmTeamId && (
                <SwarmPanel
                  embedded
                  teamId={swarmTeamId}
                  onClose={closeExecutionPanel}
                />
              )}
              {workbenchTab === "files" && (
                <WorkspaceFilePanel
                  key={threadId}
                  threadId={threadId}
                  published={thread.values.artifacts ?? []}
                />
              )}
              {(workbenchTab === "agents" || workbenchTab === "plan") && (
                <SubagentPanel
                  embedded
                  threadId={threadId}
                  sessions={sessions}
                  selectedId={selectedSessionId}
                  loading={catalog.isPending}
                  error={catalog.error}
                  mainBusy={thread.isLoading || childCommandPending}
                  state={thread.values}
                  pendingQuestion={thread.pendingQuestion}
                  tab={executionTab}
                  onTab={setExecutionTab}
                  onSelect={selectSession}
                  onContinue={continueSession}
                  onRefresh={() => void catalog.refetch()}
                  onClose={closeExecutionPanel}
                />
              )}
            </WorkspaceWorkbench>
          )}
        </div>
      </ThreadContext.Provider>
    </ChildSessionsContext.Provider>
  );
}
