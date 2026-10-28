/**
 * A QR encoder — hand-written, no dependency.
 *
 * Why we did not add a package: the web side has exactly one runtime
 * dependency (`nostr-tools`) and we wanted to keep it that way. The need
 * is narrow too: one QR is enough to carry the visitor URL to a phone.
 *
 * Scope (deliberately small):
 *  - BYTE mode only (UTF-8), which is the right mode for URLs
 *  - error correction level M only (~15% recovery)
 *  - versions 1-10 (21-57 modules per side), up to 216 bytes in byte mode
 *
 * This file DOES NOT DRAW: it only returns a matrix (true = dark module).
 * Drawing is the caller's job — which keeps this testable as a pure
 * function (vitest runs without a DOM).
 *
 * Verification: `qr-testvectors.json` was produced with a THIRD,
 * independent implementation (python `qrcode`) —
 * `scripts/gen-qr-vectors.py`. Same arrangement as the protocol vectors:
 * testing against something we produced ourselves proves nothing.
 *
 * Specification: ISO/IEC 18004.
 */

/** The generated QR code. */
export interface QRCode {
  /** Version (1-10). */
  readonly version: number;
  /** The chosen mask pattern (0-7). */
  readonly mask: number;
  /** Side length in modules: 4 * version + 17. */
  readonly size: number;
  /** modules[row][column] — true is dark. */
  readonly modules: readonly boolean[][];
}

export class QRError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "QRError";
  }
}

/** Largest supported version. */
export const MAX_VERSION = 10;

/**
 * Level M block structure, versions 1-10.
 *
 * `ec`: error correction codewords per block.
 * `blocks`: [block count, DATA codewords per block] pairs.
 */
const LEVEL_M: ReadonlyArray<{ ec: number; blocks: ReadonlyArray<[number, number]> }> = [
  { ec: 10, blocks: [[1, 16]] }, // surum 1
  { ec: 16, blocks: [[1, 28]] },
  { ec: 26, blocks: [[1, 44]] },
  { ec: 18, blocks: [[2, 32]] },
  { ec: 24, blocks: [[2, 43]] },
  { ec: 16, blocks: [[4, 27]] },
  { ec: 18, blocks: [[4, 31]] },
  {
    ec: 22,
    blocks: [
      [2, 38],
      [2, 39],
    ],
  },
  {
    ec: 22,
    blocks: [
      [3, 36],
      [2, 37],
    ],
  },
  {
    ec: 26,
    blocks: [
      [4, 43],
      [1, 44],
    ],
  }, // surum 10
];

/** Alignment pattern centres (none in version 1). */
const ALIGNMENT: ReadonlyArray<readonly number[]> = [
  [],
  [6, 18],
  [6, 22],
  [6, 26],
  [6, 30],
  [6, 34],
  [6, 22, 38],
  [6, 24, 42],
  [6, 26, 46],
  [6, 28, 50],
];

/** Number of remainder bits added after the data+EC stream. */
function remainderBits(version: number): number {
  // Versions 1 and 7-13: 0; versions 2-6: 7.
  return version >= 2 && version <= 6 ? 7 : 0;
}

/** A version's level M block structure. */
function specFor(version: number): { ec: number; blocks: ReadonlyArray<[number, number]> } {
  const spec = LEVEL_M[version - 1];
  if (!spec) throw new QRError(`unsupported version: ${version} (1-${MAX_VERSION})`);
  return spec;
}

function dataCodewords(version: number): number {
  let total = 0;
  for (const [count, data] of specFor(version).blocks) total += count * data;
  return total;
}

/**
 * Array read.
 *
 * The project is built with `noUncheckedIndexedAccess`, so every index
 * returns `| undefined`. The tables and polynomials in this file are read
 * within their bounds; instead of checking each site we use one helper.
 */
function num(arr: ArrayLike<number>, i: number): number {
  return arr[i] ?? 0;
}

// ---------------------------------------------------------------- GF(256)

/**
 * GF(256) log/antilog tables.
 *
 * Primitive polynomial 0x11d (the one the QR standard uses), generator 2.
 */
const EXP = new Uint8Array(512);
const LOG = new Uint8Array(256);
{
  let x = 1;
  for (let i = 0; i < 255; i++) {
    EXP[i] = x;
    LOG[x] = i;
    x <<= 1;
    if (x & 0x100) x ^= 0x11d;
  }
  for (let i = 255; i < 512; i++) EXP[i] = num(EXP, i - 255);
}

function gfMul(a: number, b: number): number {
  if (a === 0 || b === 0) return 0;
  return num(EXP, num(LOG, a) + num(LOG, b));
}

/** The RS generator polynomial for a given degree. */
function generatorPoly(degree: number): number[] {
  let poly = [1];
  for (let i = 0; i < degree; i++) {
    const next = new Array<number>(poly.length + 1).fill(0);
    for (let j = 0; j < poly.length; j++) {
      next[j] = num(next, j) ^ num(poly, j);
      next[j + 1] = num(next, j + 1) ^ gfMul(num(poly, j), num(EXP, i));
    }
    poly = next;
  }
  return poly;
}

/** Computes the error correction codewords of one data block. */
function reedSolomon(data: readonly number[], ecCount: number): number[] {
  const gen = generatorPoly(ecCount);
  const res = new Array<number>(ecCount).fill(0);

  for (const byte of data) {
    const factor = byte ^ num(res, 0);
    res.shift();
    res.push(0);
    for (let i = 0; i < ecCount; i++) {
      res[i] = num(res, i) ^ gfMul(num(gen, i + 1), factor);
    }
  }
  return res;
}

// ------------------------------------------------------------ bit stream

class BitBuffer {
  private readonly bits: number[] = [];

  put(value: number, length: number): void {
    for (let i = length - 1; i >= 0; i--) {
      this.bits.push((value >>> i) & 1);
    }
  }

  get length(): number {
    return this.bits.length;
  }

  /** Converts to a byte array; pads the last byte with zeros. */
  toBytes(): number[] {
    const out: number[] = [];
    for (let i = 0; i < this.bits.length; i += 8) {
      let b = 0;
      for (let j = 0; j < 8; j++) {
        b = (b << 1) | num(this.bits, i + j);
      }
      out.push(b);
    }
    return out;
  }
}

/** Width of the character count field (byte mode). */
function charCountBits(version: number): number {
  return version <= 9 ? 8 : 16;
}

/** Smallest version that can carry the given byte length. */
function pickVersion(byteLength: number): number {
  for (let v = 1; v <= MAX_VERSION; v++) {
    const capacityBits = dataCodewords(v) * 8;
    const needed = 4 + charCountBits(v) + byteLength * 8;
    if (needed <= capacityBits) return v;
  }
  throw new QRError(
    `text is too long: ${byteLength} bytes, version ${MAX_VERSION} in byte mode holds at most ${dataCodewords(MAX_VERSION) - 3} bytes`,
  );
}

/** Builds the data codewords: mode + count + data + terminator + padding. */
function encodeData(bytes: readonly number[], version: number): number[] {
  const buf = new BitBuffer();
  buf.put(0b0100, 4); // byte modu
  buf.put(bytes.length, charCountBits(version));
  for (const b of bytes) buf.put(b, 8);

  const capacity = dataCodewords(version) * 8;
  // The terminator is at most 4 bits, and only as much as fits.
  buf.put(0, Math.min(4, capacity - buf.length));
  // Align to a byte boundary.
  if (buf.length % 8 !== 0) buf.put(0, 8 - (buf.length % 8));

  const words = buf.toBytes();
  // The pad bytes are fixed by the standard and alternate: 0xEC, 0x11.
  const pad = [0xec, 0x11];
  for (let i = 0; words.length < dataCodewords(version); i++) {
    words.push(num(pad, i % 2));
  }
  return words;
}

/**
 * Splits into blocks, computes EC and interleaves in the order the
 * standard requires.
 *
 * Interleaving is a real requirement: when damage concentrates in one
 * area it should touch every block a little rather than ruin a single
 * one.
 */
function interleave(words: readonly number[], version: number): number[] {
  const spec = specFor(version);

  const dataBlocks: number[][] = [];
  const ecBlocks: number[][] = [];
  let offset = 0;
  for (const [count, dataLen] of spec.blocks) {
    for (let i = 0; i < count; i++) {
      const block = words.slice(offset, offset + dataLen);
      offset += dataLen;
      dataBlocks.push(block);
      ecBlocks.push(reedSolomon(block, spec.ec));
    }
  }

  const out: number[] = [];
  const maxData = Math.max(...dataBlocks.map((b) => b.length));
  for (let i = 0; i < maxData; i++) {
    for (const block of dataBlocks) {
      if (i < block.length) out.push(num(block, i));
    }
  }
  for (let i = 0; i < spec.ec; i++) {
    for (const block of ecBlocks) out.push(num(block, i));
  }
  return out;
}

// ----------------------------------------------------------------- matrix

/** Module state: null means not written yet (neither function pattern nor data). */
type Grid = Array<Array<boolean | null>>;

function emptyGrid(size: number): Grid {
  return Array.from({ length: size }, () => new Array<boolean | null>(size).fill(null));
}

/** Writes one module into the grid; out-of-bounds writes are ignored silently. */
function gset(grid: Grid, r: number, c: number, value: boolean | null): void {
  const row = grid[r];
  if (row) row[c] = value;
}

/** Reads from the grid; undefined out of bounds. */
function gget(grid: Grid, r: number, c: number): boolean | null | undefined {
  return grid[r]?.[c];
}

function placeFinder(grid: Grid, row: number, col: number): void {
  for (let r = -1; r <= 7; r++) {
    for (let c = -1; c <= 7; c++) {
      const rr = row + r;
      const cc = col + c;
      if (rr < 0 || rr >= grid.length || cc < 0 || cc >= grid.length) continue;
      const inOuter = r >= 0 && r <= 6 && (c === 0 || c === 6);
      const inOuter2 = c >= 0 && c <= 6 && (r === 0 || r === 6);
      const inCenter = r >= 2 && r <= 4 && c >= 2 && c <= 4;
      gset(grid, rr, cc, inOuter || inOuter2 || inCenter);
    }
  }
}

function placeFunctionPatterns(grid: Grid, version: number): void {
  const size = grid.length;

  // Three finder patterns plus their separators.
  placeFinder(grid, 0, 0);
  placeFinder(grid, 0, size - 7);
  placeFinder(grid, size - 7, 0);

  // Timing patterns (row 6 and column 6).
  for (let i = 8; i < size - 8; i++) {
    const dark = i % 2 === 0;
    gset(grid, 6, i, dark);
    gset(grid, i, 6, dark);
  }

  // Alignment patterns.
  const centers = ALIGNMENT[version - 1] ?? [];
  for (const r of centers) {
    for (const c of centers) {
      // Those landing on the finder patterns are skipped.
      if ((r === 6 && c === 6) || (r === 6 && c === size - 7) || (r === size - 7 && c === 6)) {
        continue;
      }
      for (let dr = -2; dr <= 2; dr++) {
        for (let dc = -2; dc <= 2; dc++) {
          const ring = Math.max(Math.abs(dr), Math.abs(dc));
          gset(grid, r + dr, c + dc, ring !== 1);
        }
      }
    }
  }

  // The dark module.
  gset(grid, size - 8, 8, true);

  // Reserve the format information areas (the values come later).
  reserveFormatAreas(grid);

  // Version information areas (version 7+).
  if (version >= 7) {
    for (let i = 0; i < 18; i++) {
      const a = Math.floor(i / 3);
      const b = (i % 3) + size - 11;
      gset(grid, a, b, false);
      gset(grid, b, a, false);
    }
  }
}

function reserveFormatAreas(grid: Grid): void {
  const size = grid.length;
  for (let i = 0; i <= 8; i++) {
    if (i !== 6) {
      if (gget(grid, 8, i) === null) gset(grid, 8, i, false);
      if (gget(grid, i, 8) === null) gset(grid, i, 8, false);
    }
  }
  for (let i = 0; i < 8; i++) {
    if (gget(grid, 8, size - 1 - i) === null) gset(grid, 8, size - 1 - i, false);
    if (gget(grid, size - 1 - i, 8) === null) gset(grid, size - 1 - i, 8, false);
  }
}

/**
 * Places the data bits in a zigzag.
 *
 * Two-module wide columns, right to left; the direction flips in each
 * column. Column 6 is skipped because it is the timing pattern.
 */
function placeData(grid: Grid, bits: readonly number[]): void {
  const size = grid.length;
  let bitIndex = 0;
  let upward = true;

  let right = size - 1;
  while (right >= 1) {
    // Column 6 is the timing pattern: from there on the column pairs
    // shift one to the left, so the counter has to shift too (otherwise
    // the following pairs would overlap).
    if (right === 6) right = 5;

    for (let i = 0; i < size; i++) {
      const row = grid[upward ? size - 1 - i : i];
      if (!row) continue;
      for (const c of [right, right - 1]) {
        if (row[c] !== null) continue;
        row[c] = (bits[bitIndex] ?? 0) === 1;
        bitIndex++;
      }
    }
    upward = !upward;
    right -= 2;
  }
}

/** Mask formulas (ISO/IEC 18004 Table 10). */
const MASKS: ReadonlyArray<(r: number, c: number) => boolean> = [
  (r, c) => (r + c) % 2 === 0,
  (r) => r % 2 === 0,
  (_r, c) => c % 3 === 0,
  (r, c) => (r + c) % 3 === 0,
  (r, c) => (Math.floor(r / 2) + Math.floor(c / 3)) % 2 === 0,
  (r, c) => ((r * c) % 2) + ((r * c) % 3) === 0,
  (r, c) => (((r * c) % 2) + ((r * c) % 3)) % 2 === 0,
  (r, c) => (((r + c) % 2) + ((r * c) % 3)) % 2 === 0,
];

/** Applies the mask to every module that is not a function pattern. */
function applyMask(grid: Grid, functionMap: boolean[][], mask: number): boolean[][] {
  const size = grid.length;
  const formula = MASKS[mask];
  if (!formula) throw new QRError(`mask must be 0-7, got: ${mask}`);

  const out: boolean[][] = [];
  for (let r = 0; r < size; r++) {
    const gridRow = grid[r] ?? [];
    const mapRow = functionMap[r] ?? [];
    const row: boolean[] = [];
    for (let c = 0; c < size; c++) {
      const value = gridRow[c] === true;
      row.push(mapRow[c] ? value : value !== formula(r, c));
    }
    out.push(row);
  }
  return out;
}

/** BCH(15,5) format information; the indicator for level M is 00. */
function formatBits(mask: number): number {
  const data = (0b00 << 3) | mask;
  let rem = data << 10;
  for (let i = 4; i >= 0; i--) {
    if ((rem >>> (i + 10)) & 1) rem ^= 0x537 << i;
  }
  return ((data << 10) | rem) ^ 0x5412;
}

/** BCH(18,6) version information (version 7+). */
function versionBits(version: number): number {
  let rem = version << 12;
  for (let i = 5; i >= 0; i--) {
    if ((rem >>> (i + 12)) & 1) rem ^= 0x1f25 << i;
  }
  return (version << 12) | rem;
}

/** Writes one module; out-of-bounds writes are ignored silently. */
function put(modules: boolean[][], r: number, c: number, value: boolean): void {
  const row = modules[r];
  if (row) row[c] = value;
}

function writeFormatInfo(modules: boolean[][], mask: number): void {
  const size = modules.length;
  const bits = formatBits(mask);

  for (let i = 0; i < 15; i++) {
    const on = ((bits >>> i) & 1) === 1;

    // Top-left corner: the low bits run down COLUMN 8, the high bits run
    // right to left along ROW 8 (ISO/IEC 18004 Figure 19).
    if (i < 6) put(modules, i, 8, on);
    else if (i === 6) put(modules, 7, 8, on);
    else if (i === 7) put(modules, 8, 8, on);
    else if (i === 8) put(modules, 8, 7, on);
    else put(modules, 8, 14 - i, on);

    // The copy: low bits from the right in row 8, high bits from the
    // bottom in column 8.
    if (i < 8) put(modules, 8, size - 1 - i, on);
    else put(modules, size - 15 + i, 8, on);
  }
  // The dark module is never masked.
  put(modules, size - 8, 8, true);
}

function writeVersionInfo(modules: boolean[][], version: number): void {
  if (version < 7) return;
  const size = modules.length;
  const bits = versionBits(version);
  for (let i = 0; i < 18; i++) {
    const on = ((bits >>> i) & 1) === 1;
    const a = Math.floor(i / 3);
    const b = (i % 3) + size - 11;
    put(modules, a, b, on);
    put(modules, b, a, on);
  }
}

// --------------------------------------------------------------- penalty

/** Penalty used to choose the mask (ISO/IEC 18004 Table 11). */
export function penalty(modules: readonly boolean[][]): number {
  const size = modules.length;
  let score = 0;

  /** An out-of-bounds read counts as a light module (so edge penalties stay sane). */
  const cell = (r: number, c: number): boolean => modules[r]?.[c] === true;

  // Rule 1: runs of five or more modules of the same colour.
  const runScore = (run: number) => (run >= 5 ? 3 + (run - 5) : 0);
  for (let r = 0; r < size; r++) {
    let run = 1;
    for (let c = 1; c < size; c++) {
      if (cell(r, c) === cell(r, c - 1)) run++;
      else {
        score += runScore(run);
        run = 1;
      }
    }
    score += runScore(run);
  }
  for (let c = 0; c < size; c++) {
    let run = 1;
    for (let r = 1; r < size; r++) {
      if (cell(r, c) === cell(r - 1, c)) run++;
      else {
        score += runScore(run);
        run = 1;
      }
    }
    score += runScore(run);
  }

  // Rule 2: 2x2 blocks of the same colour.
  for (let r = 0; r < size - 1; r++) {
    for (let c = 0; c < size - 1; c++) {
      const v = cell(r, c);
      if (cell(r, c + 1) === v && cell(r + 1, c) === v && cell(r + 1, c + 1) === v) {
        score += 3;
      }
    }
  }

  // Rule 3: the 1:1:3:1:1 run that looks like a finder pattern (both
  // directions).
  const pattern = [true, false, true, true, true, false, true, false, false, false, false];
  const matches = (get: (i: number) => boolean, start: number): boolean => {
    for (let i = 0; i < 11; i++) {
      if (get(start + i) !== (pattern[i] === true)) return false;
    }
    return true;
  };
  const reversed = [...pattern].reverse();
  const matchesRev = (get: (i: number) => boolean, start: number): boolean => {
    for (let i = 0; i < 11; i++) {
      if (get(start + i) !== (reversed[i] === true)) return false;
    }
    return true;
  };
  for (let r = 0; r < size; r++) {
    for (let c = 0; c + 11 <= size; c++) {
      const get = (i: number) => cell(r, i);
      if (matches(get, c) || matchesRev(get, c)) score += 40;
    }
  }
  for (let c = 0; c < size; c++) {
    for (let r = 0; r + 11 <= size; r++) {
      const get = (i: number) => cell(i, c);
      if (matches(get, r) || matchesRev(get, r)) score += 40;
    }
  }

  // Rule 4: how far the dark module ratio strays from 50%.
  let dark = 0;
  for (const row of modules) {
    for (const v of row) if (v) dark++;
  }
  const ratio = (dark * 100) / (size * size);
  score += Math.floor(Math.abs(ratio - 50) / 5) * 10;

  return score;
}

// ----------------------------------------------------------------- input

/**
 * Turns text into a QR code.
 *
 * @param text the text to encode (converted to UTF-8 bytes)
 * @param opts.mask forces the mask (0-7). Chosen by the penalty rules when
 *   omitted. Forcing it is for the test vectors: a byte-for-byte
 *   comparison with the third implementation only means something with a
 *   fixed mask.
 */
export function encode(text: string, opts?: { readonly mask?: number }): QRCode {
  if (text === "") throw new QRError("empty text cannot be encoded");

  const bytes = Array.from(new TextEncoder().encode(text));
  const version = pickVersion(bytes.length);
  const size = 4 * version + 17;

  const codewords = interleave(encodeData(bytes, version), version);
  const bits: number[] = [];
  for (const word of codewords) {
    for (let i = 7; i >= 0; i--) bits.push((word >>> i) & 1);
  }
  for (let i = 0; i < remainderBits(version); i++) bits.push(0);

  const grid = emptyGrid(size);
  placeFunctionPatterns(grid, version);

  // Map of the function patterns: the mask only applies to data modules.
  const functionMap = grid.map((row) => row.map((cell) => cell !== null));

  placeData(grid, bits);

  const forced = opts?.mask;
  if (forced !== undefined && (!Number.isInteger(forced) || forced < 0 || forced > 7)) {
    throw new QRError(`mask must be 0-7, got: ${forced}`);
  }

  let best: boolean[][] | null = null;
  let bestMask = forced ?? 0;
  let bestScore = Number.POSITIVE_INFINITY;

  const candidates = forced !== undefined ? [forced] : [0, 1, 2, 3, 4, 5, 6, 7];
  for (const mask of candidates) {
    const modules = applyMask(grid, functionMap, mask);
    writeFormatInfo(modules, mask);
    writeVersionInfo(modules, version);
    const score = penalty(modules);
    if (score < bestScore) {
      bestScore = score;
      bestMask = mask;
      best = modules;
    }
  }
  if (!best) throw new QRError("no mask could be chosen");

  return { version, mask: bestMask, size, modules: best };
}
