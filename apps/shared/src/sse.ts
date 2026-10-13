// A spec-following text/event-stream parser (https://html.spec.whatwg.org/#event-stream-interpretation).
// EventSource cannot send an Authorization header, so the clients read GET /api/w/events with
// fetch and feed the body through this.

export interface SseEvent {
  event: string; // "message" when the block has no event: line
  data: string;
  id?: string;
}

export class SseParser {
  private buf = "";
  private data: string[] = [];
  private event = "";
  private id: string | undefined;
  private started = false;
  /** The last `retry:` the server sent, in ms. */
  retry: number | undefined;
  /** The last event id seen (sticky, as in EventSource). */
  lastEventId: string | undefined;

  /** Feed a decoded chunk; returns the events it completed. */
  push(chunk: string): SseEvent[] {
    if (!this.started) {
      this.started = true;
      if (chunk.charCodeAt(0) === 0xfeff) chunk = chunk.slice(1);
    }
    this.buf += chunk;
    const out: SseEvent[] = [];
    for (;;) {
      const nl = this.findEol();
      if (nl < 0) break;
      const line = this.buf.slice(0, nl);
      // \r\n counts as one terminator; a lone \r at the very end may still be followed by \n.
      let skip = 1;
      if (this.buf[nl] === "\r") {
        if (nl + 1 >= this.buf.length) break; // wait: the \n may be in the next chunk
        if (this.buf[nl + 1] === "\n") skip = 2;
      }
      this.buf = this.buf.slice(nl + skip);
      const ev = this.line(line);
      if (ev) out.push(ev);
    }
    return out;
  }

  /** Flush at end of stream. Per spec an unterminated final block is discarded. */
  end(): SseEvent[] {
    this.buf = "";
    this.data = [];
    this.event = "";
    return [];
  }

  private findEol(): number {
    for (let i = 0; i < this.buf.length; i++) {
      const c = this.buf[i];
      if (c === "\n" || c === "\r") return i;
    }
    return -1;
  }

  private line(line: string): SseEvent | null {
    if (line === "") {
      if (this.data.length === 0) {
        this.event = "";
        return null;
      }
      const ev: SseEvent = { event: this.event || "message", data: this.data.join("\n") };
      if (this.lastEventId !== undefined) ev.id = this.lastEventId;
      this.data = [];
      this.event = "";
      return ev;
    }
    if (line.startsWith(":")) return null; // comment / keep-alive
    const colon = line.indexOf(":");
    let field = line;
    let value = "";
    if (colon >= 0) {
      field = line.slice(0, colon);
      value = line.slice(colon + 1);
      if (value.startsWith(" ")) value = value.slice(1);
    }
    switch (field) {
      case "event":
        this.event = value;
        break;
      case "data":
        this.data.push(value);
        break;
      case "id":
        if (!value.includes("\0")) this.lastEventId = value;
        break;
      case "retry":
        if (/^\d+$/.test(value)) this.retry = Number(value);
        break;
    }
    return null;
  }
}

/** Reads a fetch body as SSE events. Ends when the stream ends or the signal aborts. */
export async function* readSse(body: ReadableStream<Uint8Array>, parser = new SseParser()): AsyncGenerator<SseEvent> {
  const reader = body.getReader();
  const dec = new TextDecoder();
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      for (const ev of parser.push(dec.decode(value, { stream: true }))) yield ev;
    }
    const tail = dec.decode();
    if (tail) for (const ev of parser.push(tail)) yield ev;
    parser.end();
  } finally {
    reader.releaseLock();
  }
}
