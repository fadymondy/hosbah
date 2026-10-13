import { describe, expect, it } from "vitest";
import { readSse, SseParser } from "../sse";

describe("SseParser", () => {
  it("parses the API's framing (retry, named events, JSON data)", () => {
    const p = new SseParser();
    const evs = p.push('retry: 3000\n\nevent: notifications\ndata: {"running":null}\n\n');
    expect(p.retry).toBe(3000);
    expect(evs).toEqual([{ event: "notifications", data: '{"running":null}' }]);
  });

  it("buffers events split across chunks, including a split CRLF", () => {
    const p = new SseParser();
    expect(p.push("event: line")).toEqual([]);
    expect(p.push('\r\ndata: {"id":"a"}\r')).toEqual([]);
    expect(p.push("\n\r")).toEqual([]);
    expect(p.push("\n")).toEqual([{ event: "line", data: '{"id":"a"}' }]);
  });

  it("joins multi-line data, ignores comments, strips one leading space, defaults the name", () => {
    const p = new SseParser();
    const evs = p.push(": keep-alive\ndata:a\ndata:  b\nid: 7\n\n");
    expect(evs).toEqual([{ event: "message", data: "a\n b", id: "7" }]);
  });

  it("drops blocks without data and an unterminated tail", () => {
    const p = new SseParser();
    expect(p.push("event: x\n\nevent: y\ndata: 1")).toEqual([]);
    expect(p.end()).toEqual([]);
  });

  it("strips a leading BOM and handles bare CR line endings", () => {
    const p = new SseParser();
    expect(p.push("﻿data: hi\r\rdata: x\r")).toEqual([{ event: "message", data: "hi" }]);
  });

  it("reads a byte stream with multi-byte characters split across chunks", async () => {
    const bytes = new TextEncoder().encode('event: line\ndata: {"description":"مرحبا"}\n\n');
    const body = new ReadableStream<Uint8Array>({
      start(c) {
        c.enqueue(bytes.slice(0, 40));
        c.enqueue(bytes.slice(40));
        c.close();
      },
    });
    const out = [];
    for await (const ev of readSse(body)) out.push(ev);
    expect(out).toHaveLength(1);
    expect(JSON.parse(out[0]!.data).description).toBe("مرحبا");
  });
});
