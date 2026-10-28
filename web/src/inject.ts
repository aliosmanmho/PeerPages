/**
 * The transform that injects a `<script>` into an HTML stream (phase 6).
 *
 * With `--inject-ws-shim` the agent adds an `x-peerpages-ws-shim: 1`
 * header to HTML responses; it does NOT touch the body. The Service Worker
 * does the injection, because it is the side that knows the scope prefix
 * (`/PeerPages/`), which keeps `internal/proxy` byte-for-byte transparent
 * (BRIEF section 8).
 *
 * Three things it has to handle:
 *  1. the `<head>` tag may be split across two frames -> a bounded buffer,
 *  2. there may be no `<head>` -> before `<body>`, or at the very start,
 *  3. the encoding is unknown -> we only write ASCII bytes, which holds in
 *     every ASCII-superset encoding (UTF-8, latin-1, windows-1254).
 */

/**
 * Most bytes to buffer while looking for the injection point.
 *
 * The cap is MANDATORY: if `<head>` never arrives the stream would sit in
 * this buffer and the page would look like it never loads. Once the cap is
 * reached the script is written at the very start.
 */
export const INJECT_SCAN_LIMIT = 8 * 1024;

/** The state machine that looks for the injection point. */
export interface InjectPlan {
  /** Byte index the script goes at. */
  readonly offset: number;
  /** Whether the decision is final (no need to wait for more bytes). */
  readonly decided: boolean;
}

const HEAD_OPEN = "<head";
const BODY_OPEN = "<body";

/**
 * Finds the injection point from the start of the buffer.
 *
 * If `<head ...>` is found, right after its CLOSING angle bracket;
 * otherwise the start of `<body`; with neither, undecided (more bytes
 * needed), or 0 once the cap is reached.
 *
 * ASCII case-insensitive; nothing beyond the tag name is parsed (writing
 * an HTML parser would be far more than this job needs).
 */
export function planInjection(text: string, atEnd: boolean): InjectPlan {
  const lower = text.toLowerCase();

  const head = lower.indexOf(HEAD_OPEN);
  if (head >= 0) {
    const close = lower.indexOf(">", head);
    if (close >= 0) return { offset: close + 1, decided: true };
    // `<head` is there but `>` has not arrived yet: wait.
    return { offset: 0, decided: false };
  }

  const body = lower.indexOf(BODY_OPEN);
  if (body >= 0) {
    // No `<head` but there is a `<body`: put the script before body.
    return { offset: body, decided: true };
  }

  if (atEnd || text.length >= INJECT_SCAN_LIMIT) {
    // Neither head nor body: put it at the start. Works on broken or
    // partial HTML too.
    return { offset: 0, decided: true };
  }
  return { offset: 0, decided: false };
}

/**
 * Builds a `TransformStream` that injects the given script tag into an
 * HTML stream.
 *
 * `src` must already be absolute or relative to the scope; it is not
 * rewritten here.
 */
export function injectScriptStream(
  src: string,
): TransformStream<Uint8Array<ArrayBuffer>, Uint8Array<ArrayBuffer>> {
  const tag: Uint8Array<ArrayBuffer> = new TextEncoder().encode(
    // Right after `<head>` and NOT deferred: it has to run before the
    // page's own scripts, or they grab WebSocket first.
    `<script src="${src}"></script>`,
  );

  let buffer = new Uint8Array(0) as Uint8Array<ArrayBuffer>;
  let injected = false;
  const decoder = new TextDecoder("utf-8", { fatal: false });

  const flush = (
    controller: TransformStreamDefaultController<Uint8Array<ArrayBuffer>>,
    atEnd: boolean,
  ): void => {
    if (injected) return;

    // We only look for ASCII tag names; a broken multi-byte sequence
    // does not change the outcome.
    const text = decoder.decode(buffer, { stream: !atEnd });
    const plan = planInjection(text, atEnd);
    if (!plan.decided) return;

    // Because the search is ASCII, the text index is NOT the byte index:
    // multi-byte characters shift it. So the same position is recomputed
    // at the byte level.
    const offset = byteOffsetOf(buffer, plan.offset, text);
    controller.enqueue(buffer.subarray(0, offset));
    controller.enqueue(tag);
    controller.enqueue(buffer.subarray(offset));
    buffer = new Uint8Array(0);
    injected = true;
  };

  return new TransformStream<Uint8Array<ArrayBuffer>, Uint8Array<ArrayBuffer>>({
    transform(chunk, controller) {
      if (injected) {
        controller.enqueue(chunk);
        return;
      }
      buffer = concat(buffer, chunk);
      flush(controller, false);
    },
    flush(controller) {
      if (injected) return;
      flush(controller, true);
      if (!injected && buffer.byteLength > 0) {
        // Undecided (empty or very short body): pass it through
        // untouched.
        controller.enqueue(buffer);
      }
    },
  });
}

/**
 * Converts a text index to a byte index.
 *
 * In ASCII bodies the two are the same; with multi-byte characters the
 * UTF-8 length of the text up to that point is used.
 */
function byteOffsetOf(buffer: Uint8Array, textIndex: number, text: string): number {
  if (textIndex === 0) return 0;
  const prefix = text.slice(0, textIndex);
  const bytes = new TextEncoder().encode(prefix).byteLength;
  return Math.min(bytes, buffer.byteLength);
}

function concat(a: Uint8Array<ArrayBuffer>, b: Uint8Array<ArrayBuffer>): Uint8Array<ArrayBuffer> {
  if (a.byteLength === 0) return b;
  const out = new Uint8Array(a.byteLength + b.byteLength);
  out.set(a, 0);
  out.set(b, a.byteLength);
  return out as Uint8Array<ArrayBuffer>;
}
