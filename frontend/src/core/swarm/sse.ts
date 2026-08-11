export interface ParsedServerEvent {
  event: string;
  data: string;
  id?: string;
}

/** Incrementally decodes the subset of the SSE protocol used by the Gateway. */
export class ServerEventDecoder {
  private buffer = "";
  private event = "";
  private data: string[] = [];
  private id: string | undefined;

  push(chunk: string): ParsedServerEvent[] {
    this.buffer += chunk;
    const events: ParsedServerEvent[] = [];
    let newline = this.buffer.indexOf("\n");
    while (newline >= 0) {
      let line = this.buffer.slice(0, newline);
      this.buffer = this.buffer.slice(newline + 1);
      if (line.endsWith("\r")) line = line.slice(0, -1);
      this.consumeLine(line, events);
      newline = this.buffer.indexOf("\n");
    }
    return events;
  }

  finish(): ParsedServerEvent[] {
    const events: ParsedServerEvent[] = [];
    if (this.buffer !== "") {
      const line = this.buffer.endsWith("\r")
        ? this.buffer.slice(0, -1)
        : this.buffer;
      this.buffer = "";
      this.consumeLine(line, events);
    }
    this.dispatch(events);
    return events;
  }

  private consumeLine(line: string, events: ParsedServerEvent[]): void {
    if (line === "") {
      this.dispatch(events);
      return;
    }
    if (line.startsWith(":")) return;

    const colon = line.indexOf(":");
    const field = colon < 0 ? line : line.slice(0, colon);
    let value = colon < 0 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);

    switch (field) {
      case "event":
        this.event = value;
        break;
      case "data":
        this.data.push(value);
        break;
      case "id":
        if (!value.includes("\0")) this.id = value;
        break;
    }
  }

  private dispatch(events: ParsedServerEvent[]): void {
    if (this.data.length > 0) {
      const parsed: ParsedServerEvent = {
        event: this.event || "message",
        data: this.data.join("\n"),
      };
      if (this.id !== undefined) parsed.id = this.id;
      events.push(parsed);
    }
    this.event = "";
    this.data = [];
    this.id = undefined;
  }
}

/**
 * Tracks the Gateway's monotonically increasing message IDs. Returning false
 * means the event is a replay and must not be applied to UI state again.
 */
export class ServerEventCursor {
  private current: string | undefined;

  get value(): string | undefined {
    return this.current;
  }

  accept(candidate?: string): boolean {
    if (!candidate) return true;
    if (this.current === undefined) {
      this.current = candidate;
      return true;
    }
    if (candidate === this.current) return false;

    if (/^\d+$/.test(candidate) && /^\d+$/.test(this.current)) {
      if (BigInt(candidate) <= BigInt(this.current)) return false;
    }
    this.current = candidate;
    return true;
  }
}
