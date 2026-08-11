import { useCallback, useEffect, useRef, useState } from "react";

import { getSwarmStreamURL } from "./api";
import { ServerEventCursor, ServerEventDecoder } from "./sse";
import type {
  SwarmMessage,
  SwarmStatusEvent,
  SwarmTeamMember,
  TeammateDisplayStatus,
} from "./types";

export interface SwarmState {
  members: SwarmTeamMember[];
  messages: SwarmMessage[];
  statuses: Record<string, TeammateDisplayStatus>;
  connected: boolean;
  error: string | null;
}

const INITIAL_STATE: SwarmState = {
  members: [],
  messages: [],
  statuses: {},
  connected: false,
  error: null,
};

export function useSwarmStream(teamId: string | null): SwarmState {
  const [state, setState] = useState<SwarmState>(INITIAL_STATE);
  const generationRef = useRef(0);

  const handleEvent = useCallback((event: string, data: unknown): boolean => {
    switch (event) {
      case "team_update": {
        const members = data as SwarmTeamMember[];
        setState((s) => ({ ...s, members }));
        break;
      }
      case "message": {
        const msg = data as SwarmMessage;
        setState((s) => ({ ...s, messages: [...s.messages, msg] }));
        break;
      }
      case "status": {
        const st = data as SwarmStatusEvent;
        setState((s) => ({
          ...s,
          statuses: {
            ...s.statuses,
            [st.agent_name]: st.status as TeammateDisplayStatus,
          },
        }));
        break;
      }
      case "team_deleted":
        setState((s) => ({
          ...s,
          members: [],
          statuses: {},
          connected: false,
          error: null,
        }));
        return true;
      case "error": {
        const err = data as { error: string };
        setState((s) => ({ ...s, error: err.error }));
        break;
      }
    }
    return false;
  }, []);

  useEffect(() => {
    const generation = ++generationRef.current;
    setState(INITIAL_STATE);
    if (!teamId) return;

    let stopped = false;
    let terminal = false;
    let retryCount = 0;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController | undefined;
    const cursor = new ServerEventCursor();

    const isCurrent = () => !stopped && generationRef.current === generation;

    const applyFrames = (
      frames: ReturnType<ServerEventDecoder["push"]>,
    ): void => {
      for (const frame of frames) {
        if (!isCurrent() || !cursor.accept(frame.id)) continue;
        try {
          if (handleEvent(frame.event, JSON.parse(frame.data))) {
            terminal = true;
            controller?.abort();
            return;
          }
        } catch {
          // A malformed frame is isolated; the next complete SSE frame remains valid.
        }
      }
    };

    const scheduleRetry = (connect: () => Promise<void>): void => {
      if (!isCurrent() || terminal || retryCount >= 10) return;
      const delay = Math.min(1000 * 2 ** retryCount, 30000);
      retryCount++;
      retryTimer = setTimeout(() => {
        retryTimer = undefined;
        if (isCurrent() && !terminal) void connect();
      }, delay);
    };

    const connect = async (): Promise<void> => {
      if (!isCurrent() || terminal) return;
      controller = new AbortController();
      const requestHeaders: Record<string, string> = {
        Accept: "text/event-stream",
      };
      if (cursor.value) requestHeaders["Last-Event-ID"] = cursor.value;

      try {
        const response = await fetch(getSwarmStreamURL(teamId), {
          signal: controller.signal,
          headers: requestHeaders,
        });
        if (!response.ok) throw new Error(`SSE failed: ${response.status}`);
        if (!response.body) throw new Error("SSE response has no body");
        if (!isCurrent()) return;

        setState((current) => ({ ...current, connected: true, error: null }));
        retryCount = 0;

        const reader = response.body.getReader();
        const textDecoder = new TextDecoder();
        const eventDecoder = new ServerEventDecoder();
        while (isCurrent() && !terminal) {
          const { done, value } = await reader.read();
          if (done) {
            applyFrames(eventDecoder.push(textDecoder.decode()));
            applyFrames(eventDecoder.finish());
            break;
          }
          applyFrames(
            eventDecoder.push(textDecoder.decode(value, { stream: true })),
          );
        }
      } catch (error: unknown) {
        if (!isCurrent() || controller.signal.aborted || terminal) return;
        const message =
          error instanceof Error ? error.message : "Connection lost";
        setState((current) => ({
          ...current,
          connected: false,
          error: message,
        }));
        scheduleRetry(connect);
        return;
      }

      if (!isCurrent() || terminal || controller.signal.aborted) return;
      setState((current) => ({ ...current, connected: false }));
      scheduleRetry(connect);
    };

    void connect();
    return () => {
      stopped = true;
      controller?.abort();
      if (retryTimer !== undefined) clearTimeout(retryTimer);
    };
  }, [teamId, handleEvent]);

  return state;
}
