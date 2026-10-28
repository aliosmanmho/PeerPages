/**
 * Transport format for SDP over manual (copy-paste) signaling.
 *
 * Go counterpart: agent/internal/signal. Format:
 * base64(JSON(RTCSessionDescription)). Pasted text may carry line breaks
 * and whitespace; both are stripped.
 */

/** Upper bound for pasted text (same as the Go side). */
export const MAX_ENCODED_LEN = 256 * 1024;

const VALID_TYPES = new Set(["offer", "answer", "pranswer", "rollback"]);

export class SignalError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SignalError";
  }
}

/** Turns an SDP into a single line of base64. */
export function encodeSDP(sd: RTCSessionDescriptionInit): string {
  if (!sd.sdp) {
    throw new SignalError("empty SDP");
  }
  const json = JSON.stringify({ type: sd.type, sdp: sd.sdp });
  return btoaUtf8(json);
}

/** Turns pasted text back into an SDP. */
export function decodeSDP(pasted: string): RTCSessionDescriptionInit {
  if (pasted.length > MAX_ENCODED_LEN) {
    throw new SignalError(`pasted text is too long: ${pasted.length} characters`);
  }
  const clean = pasted.replace(/\s+/g, "");
  if (clean === "") {
    throw new SignalError("empty input");
  }

  let json: string;
  try {
    json = atobUtf8(clean);
  } catch {
    throw new SignalError("base64 could not be decoded — did you copy the whole text?");
  }

  let raw: unknown;
  try {
    raw = JSON.parse(json);
  } catch {
    throw new SignalError("the SDP JSON could not be read");
  }
  if (typeof raw !== "object" || raw === null) {
    throw new SignalError("the SDP JSON must be an object");
  }

  const obj = raw as { type?: unknown; sdp?: unknown };
  if (typeof obj.sdp !== "string" || obj.sdp === "") {
    throw new SignalError("empty SDP");
  }
  if (typeof obj.type !== "string" || !VALID_TYPES.has(obj.type)) {
    throw new SignalError(`invalid SDP type: ${String(obj.type)}`);
  }

  return { type: obj.type as RTCSdpType, sdp: obj.sdp };
}

/** UTF-8 safe base64 (btoa only accepts latin1). */
function btoaUtf8(s: string): string {
  const bytes = new TextEncoder().encode(s);
  let binary = "";
  for (const b of bytes) {
    binary += String.fromCharCode(b);
  }
  return btoa(binary);
}

function atobUtf8(s: string): string {
  const binary = atob(s);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return new TextDecoder().decode(bytes);
}
