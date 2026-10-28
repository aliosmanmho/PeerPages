export type StepId = "sw" | "signal" | "ice" | "tunnel";
export type StepState = "idle" | "active" | "done" | "error";
export type Tone = "info" | "ok" | "error";

/**
 * The status line on the connect screen. With no DOM (test environment) it
 * quietly does nothing.
 */
export class StatusView {
  private readonly steps = new Map<StepId, HTMLElement>();
  private readonly msg: HTMLElement | null;

  constructor(doc: Document = document) {
    this.msg = doc.getElementById("status-msg");
    for (const el of doc.querySelectorAll<HTMLElement>(".status__step")) {
      const id = el.dataset.step as StepId | undefined;
      if (id) this.steps.set(id, el);
    }
  }

  step(id: StepId, state: StepState): void {
    const el = this.steps.get(id);
    if (el) el.dataset.state = state;
  }

  say(text: string, tone: Tone = "info"): void {
    if (!this.msg) return;
    this.msg.textContent = text;
    this.msg.dataset.tone = tone;
  }
}
