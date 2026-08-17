import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { toast } from "sonner";

import type { PromptInputMessage } from "@/components/ai-elements/prompt-input";
import type {
  AIMessage as AIMessageType,
  MessageContentComplex,
} from "@/core/types";
import type { ThreadStream } from "@/core/types/thread";

import { getAPIClient } from "../api";
import { useUpdateSubtask } from "../tasks/context";
import { uploadFiles } from "../uploads";

import {
  type GatewayThread,
  createThread as gwCreateThread,
  deleteThread as gwDeleteThread,
  fetchThreads as gwFetchThreads,
  updateThread as gwUpdateThread,
} from "./api";
import {
  findEquivalentMessageIndex,
  mergeSSEValuesMessages,
} from "./merge-messages";
import { MessageManager } from "./message-manager";
import {
  FAIL_CLOSED_RESPONSE,
  classifyRiskLevel,
  parseMessageReplacement,
  rawRiskLevel,
  replaceAssistantMessage,
  shouldFailClosedRunEnd,
  type RunRiskVerdict,
} from "./sse-events";
import { isThreadNotFoundError } from "./thread-lifecycle";
import {
  answerUserQuestion,
  fetchActiveRunId,
  reconnectSSE,
  streamSSE,
} from "./transport";
import type {
  AgentThread,
  AgentRunIntent,
  AgentThreadState,
  PendingUserQuestion,
  TokenUsage,
} from "./types";

// ---------------------------------------------------------------------------
// useThreadHistory — load thread state for existing conversations
// ---------------------------------------------------------------------------

function useThreadHistory(
  threadId: string | null | undefined,
  isNewThread: boolean,
) {
  const [state, setState] = useState<{
    values: AgentThreadState | null;
    isLoading: boolean;
    tokenUsage: TokenUsage | null;
    notFound: boolean;
  }>({
    values: null,
    isLoading: !isNewThread && !!threadId,
    tokenUsage: null,
    notFound: false,
  });

  useEffect(() => {
    if (isNewThread || !threadId) {
      setState({
        values: null,
        isLoading: false,
        tokenUsage: null,
        notFound: false,
      });
      return;
    }

    let cancelled = false;
    setState((prev) => ({ ...prev, isLoading: true }));

    const client = getAPIClient();
    client.threads
      .getState<AgentThreadState>(threadId)
      .then((threadState) => {
        if (cancelled) return;
        let tu: TokenUsage | null = null;
        if (threadState?.token_usage) {
          const raw = threadState.token_usage;
          tu = {
            inputTokens: raw.input_tokens ?? 0,
            outputTokens: raw.output_tokens ?? 0,
            cacheReadTokens: raw.cache_read_tokens ?? 0,
            totalTokens: raw.total_tokens ?? 0,
          };
        }
        if (threadState?.values) {
          setState({
            values: threadState.values,
            isLoading: false,
            tokenUsage: tu,
            notFound: false,
          });
        } else {
          setState({
            values: null,
            isLoading: false,
            tokenUsage: tu,
            notFound: false,
          });
        }
      })
      .catch((error: unknown) => {
        if (!cancelled)
          setState({
            values: null,
            isLoading: false,
            tokenUsage: null,
            notFound: isThreadNotFoundError(error),
          });
      });

    return () => {
      cancelled = true;
    };
  }, [threadId, isNewThread]);

  return state;
}

// ---------------------------------------------------------------------------
// useSSEStream — manage SSE connection, parse events, accumulate state
//
// The stream state keeps one canonical values snapshot:
//   - NO independent `messages` state — messages live inside `values.messages`
//   - `values` events directly replace the entire values snapshot
//   - `messages` events go through MessageManager for correct chunk merging,
//     then update `values.messages` in-place
// ---------------------------------------------------------------------------

interface SSEStreamState {
  values: AgentThreadState;
  isLoading: boolean;
  error: unknown;
  tokenUsage: TokenUsage | null;
  /** Raw provider value, retained for audit/debugging. */
  riskLevel: string | null;
  /** Stable client-side classification used for safety decisions. */
  riskVerdict: RunRiskVerdict | null;
  pendingQuestion: PendingUserQuestion | null;
}

const EMPTY_STATE: AgentThreadState = {
  title: "",
  messages: [],
  artifacts: [],
};

function useSSEStream(
  threadId: string | null | undefined,
  initialValues: AgentThreadState | null,
  initialTokenUsage: TokenUsage | null,
  onCustomEvent?: (data: Record<string, unknown>) => void,
) {
  const [state, setState] = useState<SSEStreamState>({
    values: initialValues ?? EMPTY_STATE,
    isLoading: false,
    error: undefined,
    tokenUsage: initialTokenUsage,
    riskLevel: null,
    riskVerdict: null,
    pendingQuestion: null,
  });

  const abortRef = useRef<AbortController | null>(null);
  const msgManagerRef = useRef(new MessageManager());
  const onCustomEventRef = useRef(onCustomEvent);
  onCustomEventRef.current = onCustomEvent;
  // Track the run_id of the current/last stream for reconnection
  const activeRunIdRef = useRef<string | null>(null);
  // Track if we're currently reconnecting to avoid double reconnect
  const reconnectingRef = useRef(false);
  // The Go replacement payload predates message IDs. Keep the active streamed
  // AI ID so the compensation can still target the right transcript entry.
  const activeMessageIdRef = useRef<string | null>(null);
  const replacementReceivedRef = useRef(false);
  const guardrailBlockedRef = useRef(false);
  const previousThreadIdRef = useRef(threadId);

  useEffect(() => {
    if (previousThreadIdRef.current === threadId) return;
    previousThreadIdRef.current = threadId;
    abortRef.current?.abort();
    msgManagerRef.current.clear();
    activeRunIdRef.current = null;
    setState({
      values: EMPTY_STATE,
      isLoading: false,
      error: undefined,
      tokenUsage: null,
      riskLevel: null,
      riskVerdict: null,
      pendingQuestion: null,
    });
  }, [threadId]);

  useEffect(() => {
    if (initialValues) {
      setState((prev) => {
        if (prev.isLoading) return prev;
        return {
          ...prev,
          values: initialValues,
          tokenUsage: initialTokenUsage ?? prev.tokenUsage,
        };
      });
    }
  }, [initialValues, initialTokenUsage]);

  // --- Reconnection: when threadId changes back, check for active run ---
  useEffect(() => {
    if (!threadId || state.isLoading || reconnectingRef.current) return;

    let cancelled = false;
    const tryReconnect = async () => {
      try {
        const activeRunId = await fetchActiveRunId(threadId);
        if (cancelled || !activeRunId) return;

        // There's an active run — reconnect to its stream
        reconnectingRef.current = true;
        setState((prev) => ({
          ...prev,
          isLoading: true,
          error: undefined,
          riskLevel: null,
          riskVerdict: null,
        }));

        const ac = new AbortController();
        abortRef.current = ac;
        activeRunIdRef.current = activeRunId;
        msgManagerRef.current.clear();
        activeMessageIdRef.current = null;
        replacementReceivedRef.current = false;
        guardrailBlockedRef.current = false;

        try {
          for await (const { event, data } of reconnectSSE(
            threadId,
            activeRunId,
            ac.signal,
          )) {
            if (cancelled) break;
            processSSEEvent(event, data);
          }
          if (!cancelled) {
            setState((prev) => ({ ...prev, isLoading: false }));
          }
        } catch (err: unknown) {
          if (err instanceof Error && err.name === "AbortError") {
            setState((prev) => ({ ...prev, isLoading: false }));
          } else if (!cancelled) {
            console.warn("Reconnect stream ended:", err);
            setState((prev) => ({ ...prev, isLoading: false }));
          }
        } finally {
          reconnectingRef.current = false;
        }
      } catch {
        reconnectingRef.current = false;
      }
    };

    void tryReconnect();
    return () => {
      cancelled = true;
    };
    // Only run when threadId changes and we're not already streaming
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [threadId]);

  // --- Shared event processing logic ---
  const processSSEEvent = useCallback((event: string, data: unknown) => {
    const dataRecord =
      data && typeof data === "object"
        ? (data as Record<string, unknown>)
        : null;
    // Some adapters preserve runtime event names inside a custom envelope.
    // Normalize only terminal events so Python's task custom events retain
    // their existing callback behavior.
    const effectiveEvent =
      event === "custom" &&
      (dataRecord?.type === "run_end" || dataRecord?.type === "end")
        ? "end"
        : event;

    if (effectiveEvent === "metadata") {
      const meta = data as { run_id?: string };
      if (meta.run_id) {
        activeRunIdRef.current = meta.run_id;
      }
    } else if (effectiveEvent === "values") {
      const newValues = data as AgentThreadState;
      msgManagerRef.current.clear();
      flushSync(() => {
        setState((prev) => {
          const prevMsgs = prev.values.messages ?? [];
          const newMsgs = (newValues.messages ?? []).map((m) =>
            m.id ? m : { ...m, id: crypto.randomUUID() },
          );
          return {
            ...prev,
            values: {
              ...prev.values,
              ...newValues,
              messages: mergeSSEValuesMessages(prevMsgs, newMsgs),
            },
          };
        });
      });
    } else if (effectiveEvent === "messages") {
      const [chunk, meta] = data as [
        Record<string, unknown>,
        Record<string, unknown> | undefined,
      ];
      const messageId = msgManagerRef.current.add(chunk, meta);
      if (messageId) {
        const chunkType = (typeof chunk.type === "string" ? chunk.type : "")
          .replace("MessageChunk", "")
          .toLowerCase();
        if (chunkType === "ai" || chunkType === "assistant") {
          activeMessageIdRef.current = messageId;
        }
        flushSync(() => {
          setState((prev) => {
            const prevMessages = (prev.values.messages ?? []).slice();
            const pending = msgManagerRef.current.get(messageId);
            const equivalentIndex = pending
              ? findEquivalentMessageIndex(prevMessages, pending.message)
              : -1;
            const entry = msgManagerRef.current.get(
              messageId,
              equivalentIndex >= 0 ? equivalentIndex : prevMessages.length,
            );
            if (!entry) return prev;
            const { message: accumulated, index } = entry;
            prevMessages[index] = accumulated;
            return {
              ...prev,
              values: { ...prev.values, messages: prevMessages },
            };
          });
        });
      }
    } else if (
      effectiveEvent === "custom" ||
      effectiveEvent === "message_replace"
    ) {
      const customData: Record<string, unknown> = {
        ...((data && typeof data === "object" ? data : {}) as Record<
          string,
          unknown
        >),
        ...(effectiveEvent === "message_replace"
          ? { type: "message_replace" }
          : {}),
      };
      if (customData.type === "token_usage") {
        setState((prev) => {
          const incoming: TokenUsage = {
            inputTokens: (customData.input_tokens as number) ?? 0,
            outputTokens: (customData.output_tokens as number) ?? 0,
            cacheReadTokens: (customData.cache_read_tokens as number) ?? 0,
            totalTokens: (customData.total_tokens as number) ?? 0,
          };
          return { ...prev, tokenUsage: incoming };
        });
      }
      if (customData.type === "guardrail_blocked") {
        guardrailBlockedRef.current = true;
      }
      if (customData.type === "question_requested") {
        setState((prev) => ({
          ...prev,
          pendingQuestion: customData as unknown as PendingUserQuestion,
        }));
      } else if (customData.type === "question_resolved") {
        setState((prev) =>
          prev.pendingQuestion?.id === customData.id
            ? { ...prev, pendingQuestion: null }
            : prev,
        );
      }
      const replacement = parseMessageReplacement(customData);
      if (replacement) {
        replacementReceivedRef.current = true;
        let replacedMessageId: string | null = null;
        flushSync(() => {
          setState((prev) => {
            const result = replaceAssistantMessage(
              prev.values.messages ?? [],
              replacement,
              activeMessageIdRef.current,
            );
            replacedMessageId = result.messageId;
            if (result.messageId) {
              activeMessageIdRef.current = result.messageId;
            }
            return {
              ...prev,
              values: { ...prev.values, messages: result.messages },
            };
          });
        });
        if (replacedMessageId) {
          // Keep the accumulator in lock-step with React state. Otherwise a
          // late delta can append the rejected model text again.
          if (
            !msgManagerRef.current.replaceContent(
              replacedMessageId,
              replacement.content,
            )
          ) {
            msgManagerRef.current.clear();
          }
        }
      }
      onCustomEventRef.current?.(customData);
    } else if (effectiveEvent === "end" || effectiveEvent === "run_end") {
      const endData =
        data && typeof data === "object"
          ? (data as Record<string, unknown>)
          : {};
      const verdict = classifyRiskLevel(endData);
      const raw = rawRiskLevel(endData);
      const mustFailClosed = shouldFailClosedRunEnd(
        endData,
        guardrailBlockedRef.current,
        replacementReceivedRef.current,
      );

      if (mustFailClosed) {
        let replacedMessageId: string | null = null;
        flushSync(() => {
          setState((prev) => {
            const result = replaceAssistantMessage(
              prev.values.messages ?? [],
              {
                content: FAIL_CLOSED_RESPONSE,
                messageId: activeMessageIdRef.current ?? undefined,
              },
              activeMessageIdRef.current,
            );
            replacedMessageId = result.messageId;
            if (result.messageId) activeMessageIdRef.current = result.messageId;
            return {
              ...prev,
              values: { ...prev.values, messages: result.messages },
              isLoading: false,
              error: new Error("Run safety verdict is missing or blocked"),
              riskLevel: raw ?? "unknown",
              riskVerdict: verdict,
            };
          });
        });
        if (replacedMessageId) {
          msgManagerRef.current.replaceContent(
            replacedMessageId,
            FAIL_CLOSED_RESPONSE,
          );
        }
        toast.error("The response could not be verified by the safety policy.");
      } else {
        setState((prev) => ({
          ...prev,
          isLoading: false,
          riskLevel: raw,
          riskVerdict: verdict,
        }));
      }
      onCustomEventRef.current?.({
        ...endData,
        type: "run_end",
        risk_level: raw ?? "unknown",
        risk_verdict: verdict,
      });
    } else if (event === "error") {
      const errData = data as {
        error?: { message?: string };
        message?: string;
        type?: string;
      };
      const errMsg =
        errData.error?.message ?? errData.message ?? "Stream error";
      console.info(`[SSE] Backend error: ${errMsg}`);
      toast.error(errMsg);
      setState((prev) => ({
        ...prev,
        isLoading: false,
        error: new Error(errMsg),
      }));
    }
  }, []);

  const submit = useCallback(
    async (
      threadId: string,
      input: Record<string, unknown> | null | undefined,
      options?: {
        optimisticValues?: (
          prev: AgentThreadState,
        ) => Partial<AgentThreadState>;
        config?: Record<string, unknown>;
        context?: Record<string, unknown>;
      },
    ) => {
      abortRef.current?.abort();
      const ac = new AbortController();
      abortRef.current = ac;
      msgManagerRef.current.clear();
      activeRunIdRef.current = null;
      activeMessageIdRef.current = null;
      replacementReceivedRef.current = false;
      guardrailBlockedRef.current = false;

      setState((prev) => {
        const base = prev.values ?? EMPTY_STATE;
        const optimistic = options?.optimisticValues
          ? ({ ...base, ...options.optimisticValues(base) } as AgentThreadState)
          : base;
        return {
          ...prev,
          values: optimistic,
          isLoading: true,
          error: undefined,
          riskLevel: null,
          riskVerdict: null,
        };
      });

      try {
        for await (const { event, data } of streamSSE(threadId, "lead_agent", {
          input,
          config: options?.config,
          context: options?.context,
          signal: ac.signal,
        })) {
          processSSEEvent(event, data);
          if (event === "error") return;
        }

        // SSE can finish after a browser reconnect or proxy interruption has
        // dropped one or more message deltas. The persisted projection is the
        // source of truth, so reconcile it once at the terminal boundary.
        try {
          const persisted = await getAPIClient().threads.getState<AgentThreadState>(
            threadId,
          );
          if (persisted.values?.messages?.length) {
            flushSync(() => {
              setState((prev) => ({
                ...prev,
                values: {
                  ...prev.values,
                  ...persisted.values,
                  messages: mergeSSEValuesMessages(
                    prev.values.messages ?? [],
                    persisted.values.messages,
                  ),
                },
              }));
            });
          }
        } catch {
          // The streamed state remains usable when the final reconciliation
          // request races with a transient backend restart.
        }

        setState((prev) => ({ ...prev, isLoading: false }));
      } catch (err: unknown) {
        if (err instanceof Error && err.name === "AbortError") {
          setState((prev) => ({ ...prev, isLoading: false }));
          return;
        }
        console.error("SSE stream error:", err);
        toast.error(
          err instanceof Error ? err.message : "An unexpected error occurred",
        );
        setState((prev) => ({ ...prev, isLoading: false, error: err }));
      }
    },
    [processSSEEvent],
  );

  const stop = useCallback(() => {
    abortRef.current?.abort();
    abortRef.current = null;
    msgManagerRef.current.clear();
    setState((prev) => ({ ...prev, isLoading: false }));
  }, []);

  return {
    values: state.values,
    messages: state.values.messages ?? [],
    isLoading: state.isLoading,
    error: state.error,
    tokenUsage: state.tokenUsage,
    riskLevel: state.riskLevel,
    riskVerdict: state.riskVerdict,
    pendingQuestion: state.pendingQuestion,
    submit,
    stop,
  };
}

// ---------------------------------------------------------------------------
// useThreadStream — public hook consumed by page.tsx
// ---------------------------------------------------------------------------

export function useThreadStream({
  threadId,
  isNewThread,
  onFinish,
}: {
  isNewThread: boolean;
  threadId: string | null | undefined;
  onFinish?: (state: AgentThreadState) => void;
}) {
  const queryClient = useQueryClient();
  const updateSubtask = useUpdateSubtask();

  const handleCustomEvent = useCallback(
    (data: Record<string, unknown>) => {
      const type = data.type as string | undefined;
      const taskId = data.task_id as string | undefined;
      if (!taskId) return;
      if (type === "task_started") {
        updateSubtask({
          id: taskId,
          status: "in_progress",
          description: (data.description as string) ?? "",
          subagent_type: (data.subagent_type as string) ?? "general-purpose",
          prompt: "",
        });
      } else if (type === "task_running") {
        updateSubtask({
          id: taskId,
          status: "in_progress",
          latestMessage: data.message as AIMessageType | undefined,
        });
      } else if (type === "task_completed") {
        updateSubtask({
          id: taskId,
          status: "completed",
          result: (data.result as string) ?? "",
        });
      } else if (type === "task_failed" || type === "task_timed_out") {
        updateSubtask({
          id: taskId,
          status: "failed",
          error: (data.error as string) ?? "Unknown error",
        });
      }
    },
    [updateSubtask],
  );

  const history = useThreadHistory(threadId, isNewThread);
  const stream = useSSEStream(
    threadId,
    history.values,
    history.tokenUsage,
    handleCustomEvent,
  );

  const wasLoadingRef = useRef(false);
  useEffect(() => {
    if (wasLoadingRef.current && !stream.isLoading) {
      const vals = stream.values;
      onFinish?.(vals);
      if (threadId && vals.title) {
        gwUpdateThread(threadId, { title: vals.title }).catch(() => undefined);
      }
      void queryClient.invalidateQueries({ queryKey: ["gateway-threads"] });
    }
    wasLoadingRef.current = stream.isLoading;
  }, [stream.isLoading, stream.values, threadId, onFinish, queryClient]);

  const submitFn = useCallback(
    async (
      values: Record<string, unknown> | null | undefined,
      options?: {
        optimisticValues?:
          | Partial<AgentThreadState>
          | ((prev: AgentThreadState) => Partial<AgentThreadState>);
        config?: Record<string, unknown>;
        context?: Record<string, unknown>;
      },
    ) => {
      if (!threadId) throw new Error("threadId is required");
      const optFn =
        typeof options?.optimisticValues === "function"
          ? options.optimisticValues
          : options?.optimisticValues
            ? () => options.optimisticValues as Partial<AgentThreadState>
            : undefined;
      await stream.submit(threadId, values, {
        optimisticValues: optFn,
        config: options?.config,
        context: options?.context,
      });
    },
    [threadId, stream],
  );

  const answerQuestion = useCallback(
    async (answer: {
      selected?: string[];
      custom?: string;
      dismiss?: boolean;
    }) => {
      if (!stream.pendingQuestion) {
        throw new Error("No user question is pending");
      }
      await answerUserQuestion(stream.pendingQuestion, answer);
    },
    [stream.pendingQuestion],
  );

  return useMemo(
    (): ThreadStream<AgentThreadState> & {
      pendingQuestion: PendingUserQuestion | null;
      answerQuestion: typeof answerQuestion;
    } => ({
      values: stream.values,
      messages: stream.messages,
      isLoading: stream.isLoading,
      isThreadLoading: history.isLoading,
      threadNotFound: history.notFound,
      error: stream.error,
      tokenUsage: stream.tokenUsage,
      pendingQuestion: stream.pendingQuestion,
      answerQuestion,
      submit: submitFn,
      stop: stream.stop,
    }),
    [stream, history.isLoading, history.notFound, submitFn, answerQuestion],
  );
}

// ---------------------------------------------------------------------------
// useSubmitThread — builds the human message and calls thread.submit()
// ---------------------------------------------------------------------------

export function useSubmitThread({
  threadId,
  thread,
  threadContext,
  isNewThread,
  afterSubmit,
}: {
  isNewThread: boolean;
  threadId: string | null | undefined;
  thread: ThreadStream<AgentThreadState>;
  threadContext: AgentRunIntent;
  afterSubmit?: () => void;
}) {
  const queryClient = useQueryClient();
  const callback = useCallback(
    async (message: PromptInputMessage) => {
      const text = message.text.trim();
      const attachedContents: MessageContentComplex[] = [];
      let attachedText = "";

      if (message.files && message.files.length > 0) {
        try {
          const filePromises = message.files.map(async (fileUIPart) => {
            if (fileUIPart.url && fileUIPart.filename) {
              try {
                const response = await fetch(fileUIPart.url);
                const blob = await response.blob();
                return new File([blob], fileUIPart.filename, {
                  type: fileUIPart.mediaType || blob.type,
                });
              } catch (error) {
                console.error(
                  `Failed to fetch file ${fileUIPart.filename}:`,
                  error,
                );
                return null;
              }
            }
            return null;
          });

          const conversionResults = await Promise.all(filePromises);
          const files = conversionResults.filter(
            (file): file is File => file !== null,
          );
          const failedConversions = conversionResults.length - files.length;

          if (failedConversions > 0) {
            throw new Error(
              `Failed to prepare ${failedConversions} attachment(s) for upload. Please retry.`,
            );
          }

          if (!threadId) {
            throw new Error("Thread is not ready for file upload.");
          }

          if (files.length > 0) {
            const uploadRes = await uploadFiles(threadId, files);
            if (uploadRes?.files) {
              for (const fileInfo of uploadRes.files) {
                const originalFile = files.find(
                  (f) => f.name === fileInfo.filename,
                );
                const mediaType = originalFile?.type ?? "";

                if (mediaType.startsWith("image/") && originalFile) {
                  const base64DataUri = await new Promise<string>(
                    (resolve, reject) => {
                      const reader = new FileReader();
                      reader.onload = () => resolve(reader.result as string);
                      reader.onerror = reject;
                      reader.readAsDataURL(originalFile);
                    },
                  );

                  attachedContents.push({
                    type: "image_url",
                    image_url: { url: base64DataUri },
                  });
                }
              }

              const filesStr = uploadRes.files
                .map(
                  (f) =>
                    `- ${f.filename} (${f.size})\n  Path: ${f.virtual_path}`,
                )
                .join("\n");
              attachedText = `\n<uploaded_files>\n${filesStr}\n</uploaded_files>\n`;
            }
          }
        } catch (error) {
          console.error("Failed to upload files:", error);
          const errorMessage =
            error instanceof Error ? error.message : "Failed to upload files.";
          toast.error(errorMessage);
          throw error;
        }
      }

      if (isNewThread && threadId) {
        await getAPIClient().threads.create(threadId);
        try {
          await gwCreateThread({
            id: threadId,
            title: text.length > 0 ? text.slice(0, 80) : undefined,
          });
          void queryClient.invalidateQueries({ queryKey: ["gateway-threads"] });
        } catch {
          // The conversation can still proceed even if sidebar metadata fails.
        }
      }

      const humanMessage = {
        type: "human" as const,
        content: [
          ...attachedContents,
          { type: "text", text: `${text}${attachedText}` },
        ],
      };

      const submitPromise = thread.submit(
        { messages: [humanMessage] },
        {
          optimisticValues(prev: AgentThreadState) {
            const prevMessages = Array.isArray(prev?.messages)
              ? prev.messages
              : [];
            return {
              ...prev,
              messages: [
                ...prevMessages,
                { ...humanMessage, id: crypto.randomUUID() },
              ],
            } as Partial<AgentThreadState>;
          },
          config: { recursion_limit: 1000 },
          context: { ...threadContext, thread_id: threadId },
        },
      );

      afterSubmit?.();

      await submitPromise;
      void queryClient.invalidateQueries({ queryKey: ["gateway-threads"] });
    },
    [thread, isNewThread, threadId, threadContext, queryClient, afterSubmit],
  );
  return callback;
}

// ---------------------------------------------------------------------------
// Thread list hooks
// ---------------------------------------------------------------------------

function toAgentThread(gw: GatewayThread): AgentThread {
  return {
    thread_id: gw.id,
    created_at: gw.created_at,
    updated_at: gw.updated_at,
    metadata: {},
    status: "idle",
    values: {
      title: gw.title ?? "Untitled",
      messages: [],
      artifacts: [],
    },
    interrupts: {},
  };
}

export function useThreads() {
  return useQuery<AgentThread[]>({
    queryKey: ["gateway-threads"],
    queryFn: async () => {
      const threads = await gwFetchThreads({ limit: 50 });
      return threads.map(toAgentThread);
    },
  });
}

export function useDeleteThread() {
  const queryClient = useQueryClient();
  const apiClient = getAPIClient();
  return useMutation({
    mutationFn: async ({ threadId }: { threadId: string }) => {
      await apiClient.threads.delete(threadId);
      await gwDeleteThread(threadId).catch(() => undefined);
    },
    onSuccess(_, { threadId }) {
      queryClient.setQueriesData(
        { queryKey: ["gateway-threads"], exact: false },
        (oldData: Array<AgentThread>) => {
          return oldData?.filter((t) => t.thread_id !== threadId) ?? [];
        },
      );
    },
  });
}

export function useRenameThread() {
  const queryClient = useQueryClient();
  const apiClient = getAPIClient();
  return useMutation({
    mutationFn: async ({
      threadId,
      title,
    }: {
      threadId: string;
      title: string;
    }) => {
      await apiClient.threads.updateState(threadId, { values: { title } });
      await gwUpdateThread(threadId, { title }).catch(() => undefined);
    },
    onSuccess(_, { threadId, title }) {
      queryClient.setQueriesData(
        { queryKey: ["gateway-threads"], exact: false },
        (oldData: Array<AgentThread>) => {
          return (
            oldData?.map((t) => {
              if (t.thread_id === threadId) {
                return { ...t, values: { ...t.values, title } };
              }
              return t;
            }) ?? []
          );
        },
      );
    },
  });
}
