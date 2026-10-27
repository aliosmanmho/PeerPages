import { describe, expect, it, vi } from "vitest";

import { DEFAULT_BACKOFF_MS, Supervisor, type SupervisorState } from "./reconnect";

/**
 * Faz 4: tunel kopunca sayfa kendi kendine yeniden baglanmali.
 *
 * Testler gercek zamanlayici kullaniyor ama geri sayim milisaniyelere
 * indiriliyor; durum makinesi DOM'dan ve signaling'den bagimsiz.
 */

/** Olay dinleyicilerini elle tetiklenebilen kucuk bir hedef. */
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
      if (Date.now() - started > timeoutMs) return reject(new Error("zaman asimi"));
      setTimeout(tick, 5);
    };
    tick();
  });
}

describe("Supervisor", () => {
  it("kopmadan sonra yeniden baglanir", async () => {
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

    sup.markLost("baglanti-koptu");
    expect(sup.currentState).toBe("reconnecting");

    await waitFor(() => sup.currentState === "connected");
    expect(attempts).toBe(1);
    expect(sup.reconnects).toBe(1);
    sup.stop();
  });

  it("basarisiz denemeleri geri sayimla tekrarlar", async () => {
    const events = new FakeEvents();
    let attempts = 0;
    const sup = new Supervisor({
      connect: async () => {
        attempts += 1;
        if (attempts < 3) throw new Error("ajan yok");
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
    // Ucuncu denemede baglandi: tek bir yeniden baglanma sayilir.
    expect(sup.reconnects).toBe(1);
    sup.stop();
  });

  it("ayni kopma icin iki haber geri sayimi uzatmaz", async () => {
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
    // WebRTC durumu VE canlilik ping'i ayni kopmayi bildirebilir.
    sup.markLost("connectionstatechange");
    sup.markLost("ping");
    sup.markLost("ping");

    await waitFor(() => sup.currentState === "connected");
    expect(attempts).toBe(1);
    expect(states.filter((s) => s === "reconnecting").length).toBeGreaterThan(0);
    sup.stop();
  });

  // Uyku/uyanma yolu: cihaz geri gelince 30 saniye beklemenin anlami yok.
  it("online olayi beklemeyi keser", async () => {
    const events = new FakeEvents();
    let attempts = 0;
    const sup = new Supervisor({
      connect: async () => {
        attempts += 1;
        sup.markConnected();
      },
      // Uzun geri sayim: olay tetiklenmezse test zaman asimina ugrardi.
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

  it("sekme gorunur degilse uyanma olayi denemeyi baslatmaz", async () => {
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

  it("retryNow beklemeden dener", async () => {
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

  it("stop dinleyicileri ve bekleyen denemeyi birakir", async () => {
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

  it("start cagrilmadan kopma haberi bir sey baslatmaz", async () => {
    const connect = vi.fn(async () => {});
    const sup = new Supervisor({ connect, backoffMs: [5], events: new FakeEvents() });

    sup.markLost("test");
    await new Promise((r) => setTimeout(r, 40));
    expect(connect).not.toHaveBeenCalled();
    expect(sup.currentState).toBe("idle");
  });

  it("varsayilan geri sayim artarak ilerler ve 30 sn'de durur", () => {
    expect(DEFAULT_BACKOFF_MS[0]).toBe(1000);
    for (let i = 1; i < DEFAULT_BACKOFF_MS.length; i++) {
      expect(DEFAULT_BACKOFF_MS[i] ?? 0).toBeGreaterThan(DEFAULT_BACKOFF_MS[i - 1] ?? 0);
    }
    expect(DEFAULT_BACKOFF_MS[DEFAULT_BACKOFF_MS.length - 1]).toBe(30_000);
  });
});
