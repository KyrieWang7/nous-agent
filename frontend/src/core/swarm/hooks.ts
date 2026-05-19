import { useCallback, useEffect, useRef, useState } from "react";

import { getSwarmStreamURL } from "./api";
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
  const retryRef = useRef(0);
  const abortRef = useRef<AbortController | null>(null);

  const handleEvent = useCallback((event: string, data: unknown) => {
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
          statuses: { ...s.statuses, [st.agent_name]: st.status as TeammateDisplayStatus },
        }));
        break;
      }
      case "team_deleted":
        setState((s) => ({ ...s, members: [], connected: false }));
        break;
      case "error": {
        const err = data as { error: string };
        setState((s) => ({ ...s, error: err.error }));
        break;
      }
    }
  }, []);

  const connect = useCallback(async () => {
    if (!teamId) return;

    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const res = await fetch(getSwarmStreamURL(teamId), {
        signal: controller.signal,
        headers: { Accept: "text/event-stream" },
      });

      if (!res.ok) throw new Error(`SSE failed: ${res.status}`);

      setState((s) => ({ ...s, connected: true, error: null }));
      retryRef.current = 0;

      const reader = res.body!.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      let currentEvent: string | null = null;

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop() ?? "";

        for (const line of lines) {
          if (line.startsWith("event:")) {
            currentEvent = line.slice(6).trim();
          } else if (line.startsWith("data:") && currentEvent) {
            const raw = line.slice(5).trim();
            if (!raw) continue;
            try {
              handleEvent(currentEvent, JSON.parse(raw));
            } catch {
              // ignore parse errors
            }
            currentEvent = null;
          } else if (line.trim() === "") {
            currentEvent = null;
          }
        }
      }
    } catch (err: unknown) {
      if (controller.signal.aborted) return;
      const message = err instanceof Error ? err.message : "Connection lost";
      setState((s) => ({ ...s, connected: false, error: message }));

      if (retryRef.current < 10) {
        const delay = Math.min(1000 * 2 ** retryRef.current, 30000);
        retryRef.current++;
        setTimeout(() => { connect(); }, delay);
      }
    }
  }, [teamId, handleEvent]);

  useEffect(() => {
    if (teamId) {
      setState(INITIAL_STATE);
      connect();
    }
    return () => { abortRef.current?.abort(); };
  }, [teamId, connect]);

  return state;
}
