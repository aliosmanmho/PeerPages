import { describe, expect, it, vi } from "vitest";

import { DEFAULT_BACKOFF_MS, Supervisor, type SupervisorState } from "./reconnect";

/**
 * Phase 4: when the tunnel drops the page has to reconnect on its own.
 *
 * The tests use real timers but the backoff is brought down to
 * milliseconds; the state machine is free of the DOM and of signaling.
 */

/** A small event target whose listeners can be fired by hand. */
class FakeEvents implements Pick<EventTarget, "addEventListener" | "removeEventListener"> {
  private readonly handlers = new Map<string, Set<EventListenerOrEventListenerObject>>();

  addEventListener(type: string, cb: EventListenerOrEventListenerObject | null): void {
    if (!cb) return;
    const set = this.handlers.get(type) ?? new Set();
    set.add(cb);
    this.handlers.set(type, set);
  }

  removeEventListener(type: string, cb: EventListenerOrEventListenerObject | null): void {
    if (!cb) return;
    this.handlers.get(type)?.delete(cb);
  }

  fire(type: string): void {
    for (const cb of this.handlers.get(type) ?? []) {
      if (typeof cb === "function") cb(new Event(type));
      else cb.handleEvent(new Event(type));
    }
  }

  count(type: string): number {
    return this.handlers.get(type)?.size ?? 0;
  }
}

function waitFor(cond: () => boolean, timeoutMs = 2000): Promise<void> {
  return new Promise((resolve, reject) => {
    const started = Date.now();
    const tick = () => {
      if (cond()) return resolve();
      if (Date.now() - started > timeoutMs) return reject(new Error("a timeout"));
      setTimeout(tick, 5);
    };
    tick();
  });
}

describe("Supervisor", () => {
  it("it reconnects after a drop", async () => {
    const events = new FakeEvents();
    let attempts = 0;
    const sup = new Supervisor({
      connect: async () => {
        attempts += 1;
        sup.markConnected();
      },
      backoffMs: [5],
      events,
      isVisible: () => true,
    });

    sup.start();
    sup.markConnected();
    expect(sup.currentState).toBe("connected");
    expect(sup.reconnects).toBe(0);

    sup.markLost("connection-lost");
    expect(sup.currentState).toBe("reconnecting");

    await waitFor(() => sup.currentState === "connected");
    expect(attempts).toBe(1);
    expect(sup.reconnects).toBe(1);
    sup.stop();
  });

  it("it retries failed attempts with backoff", async () => {
    const events = new FakeEvents();
    let attempts = 0;
    const sup = new Supervisor({
      connect: async () => {
        attempts += 1;
        if (attempts < 3) throw new Error("no agent");
        sup.markConnected();
      },
      backoffMs: [5, 5, 5],
      events,
      isVisible: () => true,
    });

    sup.start();
    sup.markConnected();
    sup.markLost("test");

    await waitFor(() => sup.currentState === "connected");
    expect(attempts).toBe(3);
    // It connected on the third attempt: that counts as one
    // reconnect.
    expect(sup.reconnects).toBe(1);
    sup.stop();
  });

  it("two reports of the same drop do not extend the backoff", async () => {
    const events = new FakeEvents();
    const states: SupervisorState[] = [];
    let attempts = 0;
    const sup = new Supervisor({
      connect: async () => {
        attempts += 1;
        sup.markConnected();
      },
      backoffMs: [30],
      events,
      isVisible: () => true,
      onState: (s) => states.push(s),
    });

    sup.start();
    sup.markConnected();
    // The WebRTC state AND the liveness ping can both report the same
    // drop.
    sup.markLost("connectionstatechange");
    sup.markLost("ping");
    sup.markLost("ping");

    await waitFor(() => sup.currentState === "connected");
    expect(attempts).toBe(1);
    expect(states.filter((s) => s === "reconnecting").length).toBeGreaterThan(0);
    sup.stop();
  });

  // The sleep/wake path: once the device is back, waiting 30 seconds is
  // pointless.
  it("the online event stops the wait", async () => {
    const events = new FakeEvents();
    let attempts = 0;
    const sup = new Supervisor({
      connect: async () => {
        attempts += 1;
        sup.markConnected();
      },
      // A long backoff: if the event did not fire the test would time
      // out.
      backoffMs: [60_000],
      events,
      isVisible: () => true,
    });

    sup.start();
    sup.markConnected();
    sup.markLost("uyku");
    expect(attempts).toBe(0);

    events.fire("online");
    await waitFor(() => sup.currentState === "connected", 1000);
    expect(attempts).toBe(1);
    sup.stop();
  });

  it("while the tab is not visible a wake event starts no attempt", async () => {
    const events = new FakeEvents();
    let attempts = 0;
    const sup = new Supervisor({
      connect: async () => {
        attempts += 1;
        sup.markConnected();
      },
      backoffMs: [60_000],
      events,
      isVisible: () => false,
    });

    sup.start();
    sup.markConnected();
    sup.markLost("uyku");
    events.fire("visibilitychange");

    await new Promise((r) => setTimeout(r, 50));
    expect(attempts).toBe(0);
    expect(sup.currentState).toBe("reconnecting");
    sup.stop();
  });

  it("retryNow tries without waiting", async () => {
    const events = new FakeEvents();
    let attempts = 0;
    const sup = new Supervisor({
      connect: async () => {
        attempts += 1;
        sup.markConnected();
      },
      backoffMs: [60_000],
      events,
      isVisible: () => true,
    });

    sup.start();
    sup.markLost("test");
    sup.retryNow();

    await waitFor(() => sup.currentState === "connected", 1000);
    expect(attempts).toBe(1);
    sup.stop();
  });

  it("stop releases the listeners and the pending attempt", async () => {
    const events = new FakeEvents();
    let attempts = 0;
    const sup = new Supervisor({
      connect: async () => {
        attempts += 1;
      },
      backoffMs: [10],
      events,
      isVisible: () => true,
    });

    sup.start();
    expect(events.count("online")).toBe(1);
    sup.markConnected();
    sup.markLost("test");
    sup.stop();

    expect(events.count("online")).toBe(0);
    expect(events.count("visibilitychange")).toBe(0);
    await new Promise((r) => setTimeout(r, 60));
    expect(attempts).toBe(0);
    expect(sup.currentState).toBe("idle");
  });

  it("without start being called a drop report starts nothing", async () => {
    const connect = vi.fn(async () => {});
    const sup = new Supervisor({ connect, backoffMs: [5], events: new FakeEvents() });

    sup.markLost("test");
    await new Promise((r) => setTimeout(r, 40));
    expect(connect).not.toHaveBeenCalled();
    expect(sup.currentState).toBe("idle");
  });

  it("the default backoff grows and stops at 30 s", () => {
    expect(DEFAULT_BACKOFF_MS[0]).toBe(1000);
    for (let i = 1; i < DEFAULT_BACKOFF_MS.length; i++) {
      expect(DEFAULT_BACKOFF_MS[i] ?? 0).toBeGreaterThan(DEFAULT_BACKOFF_MS[i - 1] ?? 0);
    }
    expect(DEFAULT_BACKOFF_MS[DEFAULT_BACKOFF_MS.length - 1]).toBe(30_000);
  });
});
