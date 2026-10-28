/**
 * The tunnel recovering on its own (BRIEF phase 4).
 *
 * Why the visitor side does it: the agent is answerer-only and a session
 * is one-shot — a new connection only comes about when the visitor
 * publishes a new offer. On top of that, the phase 3 idle check already
 * closes the agent-side session after 30 seconds of silence, so after a
 * long drop (sleep/wake) there is no session left to rescue: every attempt
 * builds a new session FROM SCRATCH (new sid, new ephemeral keypair).
 *
 * This file is kept free of the DOM and of signaling: the function that
 * performs an attempt is passed in, so the state machine can be tested on
 * its own.
 */

export type SupervisorState = "connected" | "reconnecting" | "idle";

/** Default backoff (ms). The last value repeats. */
export const DEFAULT_BACKOFF_MS = [1000, 2000, 4000, 8000, 15000, 30000] as const;

export interface SupervisorInfo {
  /** Which attempt this is (starts at 1; 0 once connected). */
  readonly attempt: number;
  /** Time left until the next attempt (ms); 0 while one is running. */
  readonly waitMs: number;
  /** Reason for the last drop or failure. */
  readonly reason?: string;
}

export interface SupervisorOptions {
  /**
   * A single connection attempt. If it resolves the tunnel is taken to be
   * open; if it throws, it is retried with backoff.
   */
  readonly connect: () => Promise<void>;
  /** Reports state changes (for the screen). */
  readonly onState?: (state: SupervisorState, info: SupervisorInfo) => void;
  /** Backoff list; DEFAULT_BACKOFF_MS when omitted. */
  readonly backoffMs?: readonly number[];
  /**
   * Event source; `window` when omitted. Tests can pass a fake target.
   * The `online` and `visibilitychange` events RESET the backoff: both
   * mean "the network or the device came back", and that is exactly the
   * sleep/wake path.
   */
  readonly events?: Pick<EventTarget, "addEventListener" | "removeEventListener">;
  /** Whether the tab is visible; document.visibilityState when omitted. */
  readonly isVisible?: () => boolean;
}

export class Supervisor {
  private readonly opts: SupervisorOptions;
  private readonly backoff: readonly number[];
  private readonly events: Pick<EventTarget, "addEventListener" | "removeEventListener"> | null;

  private state: SupervisorState = "idle";
  private attempt = 0;
  private reconnectCount = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private running = false;
  private inFlight = false;
  private lastReason: string | undefined;

  private readonly onWake = () => {
    // Network or tab came back: stop waiting, try right away.
    if (this.state !== "reconnecting") return;
    if (!this.visible()) return;
    this.attempt = 0;
    this.retryNow();
  };

  constructor(opts: SupervisorOptions) {
    this.opts = opts;
    this.backoff = opts.backoffMs ?? DEFAULT_BACKOFF_MS;
    this.events = opts.events ?? (typeof window === "undefined" ? null : window);
  }

  /** Starts listening to events. */
  start(): void {
    if (this.running) return;
    this.running = true;
    this.events?.addEventListener("online", this.onWake);
    this.events?.addEventListener("visibilitychange", this.onWake);
  }

  /** Stops listening and cancels a pending attempt. */
  stop(): void {
    this.running = false;
    this.clearTimer();
    this.events?.removeEventListener("online", this.onWake);
    this.events?.removeEventListener("visibilitychange", this.onWake);
    this.state = "idle";
  }

  /** The tunnel opened (first connection or a reconnect). */
  markConnected(): void {
    this.clearTimer();
    const wasReconnecting = this.state === "reconnecting";
    if (wasReconnecting) this.reconnectCount += 1;
    this.state = "connected";
    this.attempt = 0;
    this.lastReason = undefined;
    this.emit();
  }

  /**
   * The tunnel dropped: starts retrying with backoff.
   *
   * More than one source can report the same drop (the WebRTC state and
   * the liveness ping), so repeated calls do not extend the backoff.
   */
  markLost(reason: string): void {
    if (!this.running) return;
    if (this.state === "reconnecting") return;
    this.state = "reconnecting";
    this.lastReason = reason;
    this.attempt = 0;
    this.schedule();
  }

  /** Try now without waiting (the user asked to retry). */
  retryNow(): void {
    if (!this.running) return;
    this.clearTimer();
    this.state = "reconnecting";
    void this.tryOnce();
  }

  get currentState(): SupervisorState {
    return this.state;
  }

  /** How many times we reconnected. */
  get reconnects(): number {
    return this.reconnectCount;
  }

  private visible(): boolean {
    if (this.opts.isVisible) return this.opts.isVisible();
    if (typeof document === "undefined") return true;
    return document.visibilityState !== "hidden";
  }

  private waitFor(): number {
    const i = Math.min(this.attempt, this.backoff.length - 1);
    return this.backoff[i] ?? 1000;
  }

  private schedule(): void {
    this.clearTimer();
    const waitMs = this.waitFor();
    this.emit(waitMs);
    this.timer = setTimeout(() => {
      this.timer = null;
      void this.tryOnce();
    }, waitMs);
  }

  private async tryOnce(): Promise<void> {
    if (!this.running || this.inFlight) return;
    this.inFlight = true;
    this.attempt += 1;
    this.emit(0);

    try {
      await this.opts.connect();
      // Success is reported through markConnected: the caller is what
      // decides the tunnel really opened.
    } catch (err) {
      this.lastReason = err instanceof Error ? err.message : String(err);
      if (this.running && this.state === "reconnecting") this.schedule();
    } finally {
      this.inFlight = false;
    }
  }

  private clearTimer(): void {
    if (this.timer !== null) {
      clearTimeout(this.timer);
      this.timer = null;
    }
  }

  private emit(waitMs = 0): void {
    this.opts.onState?.(this.state, {
      attempt: this.attempt,
      waitMs,
      ...(this.lastReason === undefined ? {} : { reason: this.lastReason }),
    });
  }
}
