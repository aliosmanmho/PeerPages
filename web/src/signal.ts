/**
 * Elle (kopyala-yapistir) signaling icin SDP tasima bicimi.
 *
 * Go karsiligi: agent/internal/signal. Bicim: base64(JSON(RTCSessionDescription)).
 * Yapistirilan metinde satir kirilmasi ve bosluk olabilir; temizlenir.
 */

/** Yapistirilan metin icin ust sinir (Go tarafiyla ayni). */
export const MAX_ENCODED_LEN = 256 * 1024;

const VALID_TYPES = new Set(["offer", "answer", "pranswer", "rollback"]);

export class SignalError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SignalError";
  }
}

/** SDP'yi tek satirlik base64 metne cevirir. */
export function encodeSDP(sd: RTCSessionDescriptionInit): string {
  if (!sd.sdp) {
    throw new SignalError("SDP bos");
  }
  const json = JSON.stringify({ type: sd.type, sdp: sd.sdp });
  return btoaUtf8(json);
}

/** Yapistirilan metni SDP'ye cevirir. */
export function decodeSDP(pasted: string): RTCSessionDescriptionInit {
  if (pasted.length > MAX_ENCODED_LEN) {
    throw new SignalError(`yapistirilan metin cok uzun: ${pasted.length} karakter`);
  }
  const clean = pasted.replace(/\s+/g, "");
  if (clean === "") {
    throw new SignalError("bos girdi");
  }

  let json: string;
  try {
    json = atobUtf8(clean);
  } catch {
    throw new SignalError("base64 cozulemedi — metni eksiksiz kopyaladin mi?");
  }

  let raw: unknown;
  try {
    raw = JSON.parse(json);
  } catch {
    throw new SignalError("SDP JSON'u okunamadi");
  }
  if (typeof raw !== "object" || raw === null) {
    throw new SignalError("SDP JSON'u bir nesne olmali");
  }

  const obj = raw as { type?: unknown; sdp?: unknown };
  if (typeof obj.sdp !== "string" || obj.sdp === "") {
    throw new SignalError("SDP bos");
  }
  if (typeof obj.type !== "string" || !VALID_TYPES.has(obj.type)) {
    throw new SignalError(`gecersiz SDP tipi: ${String(obj.type)}`);
  }

  return { type: obj.type as RTCSdpType, sdp: obj.sdp };
}

/** UTF-8 guvenli base64 kodlama (btoa yalnizca latin1 kabul eder). */
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
