/**
 * QR kodlayici — elle yazilmis, bagimlilik yok.
 *
 * Neden paket eklemedik: web tarafinda tek calisma zamani bagimliligi
 * (`nostr-tools`) var ve oyle kalsin istedik. Ihtiyac da dar: ziyaretci
 * URL'sini telefona tasimak icin tek bir QR yetiyor.
 *
 * Kapsam (bilincli olarak kucuk):
 *  - yalnizca BYTE modu (UTF-8), ki URL'ler icin dogru mod bu
 *  - yalnizca hata duzeltme seviyesi M (~%15 kurtarma)
 *  - surum 1-10 (kenar 21-57 modul), byte modunda 216 bayta kadar
 *
 * Bu dosya CIZIM YAPMAZ: yalnizca matris dondurur (true = koyu modul).
 * Cizim cagiran tarafta — boylece burasi saf fonksiyon olarak test
 * edilebiliyor (vitest DOM'suz kosuyor).
 *
 * Dogrulama: `qr-testvectors.json` UCUNCU bir bagimsiz uygulama (python
 * `qrcode`) ile uretildi — `scripts/gen-qr-vectors.py`. Protokol
 * vektorlerindeki ayni duzen: kendi uretttigimiz seye karsi test etmek bir
 * sey kanitlamaz.
 *
 * Tanim: ISO/IEC 18004.
 */

/** Uretilen QR kodu. */
export interface QRCode {
  /** Surum (1-10). */
  readonly version: number;
  /** Secilen maske deseni (0-7). */
  readonly mask: number;
  /** Kenar uzunlugu (modul): 4 * surum + 17. */
  readonly size: number;
  /** modules[satir][kolon] — true koyu. */
  readonly modules: readonly boolean[][];
}

export class QRError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "QRError";
  }
}

/** Desteklenen en buyuk surum. */
export const MAX_VERSION = 10;

/**
 * Seviye M blok yapisi, surum 1-10.
 *
 * `ec`: blok basina hata duzeltme kod sozcugu.
 * `blocks`: [blok sayisi, blok basina VERI kod sozcugu] ciftleri.
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

/** Hizalama deseni merkezleri (surum 1'de yok). */
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

/** Veri+EC akisindan sonra eklenen dolgu biti sayisi. */
function remainderBits(version: number): number {
  // Surum 1 ve 7-13: 0; surum 2-6: 7.
  return version >= 2 && version <= 6 ? 7 : 0;
}

/** Surumun seviye M blok yapisi. */
function specFor(version: number): { ec: number; blocks: ReadonlyArray<[number, number]> } {
  const spec = LEVEL_M[version - 1];
  if (!spec) throw new QRError(`desteklenmeyen surum: ${version} (1-${MAX_VERSION})`);
  return spec;
}

function dataCodewords(version: number): number {
  let total = 0;
  for (const [count, data] of specFor(version).blocks) total += count * data;
  return total;
}

/**
 * Dizi okumasi.
 *
 * Proje `noUncheckedIndexedAccess` ile derleniyor, yani her indeksleme
 * `| undefined` doner. Bu dosyadaki tablolar ve polinomlar sinirlari
 * icinde okunuyor; tek tek kontrol yerine tek bir yardimci kullaniyoruz.
 */
function num(arr: ArrayLike<number>, i: number): number {
  return arr[i] ?? 0;
}

// ---------------------------------------------------------------- GF(256)

/**
 * Galois alani GF(256) log/antilog tablolari.
 *
 * Ilkel polinom 0x11d (QR standardinin kullandigi), uretici 2.
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

/** Verilen derece icin RS uretici polinomu. */
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

/** Bir veri blogunun hata duzeltme kod sozcuklerini hesaplar. */
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

// ------------------------------------------------------------- bit akisi

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

  /** Bayt dizisine cevirir; son bayt eksikse sifirla doldurur. */
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

/** Karakter sayaci alan genisligi (byte modu). */
function charCountBits(version: number): number {
  return version <= 9 ? 8 : 16;
}

/** Verilen bayt uzunlugunu tasiyabilen en kucuk surum. */
function pickVersion(byteLength: number): number {
  for (let v = 1; v <= MAX_VERSION; v++) {
    const capacityBits = dataCodewords(v) * 8;
    const needed = 4 + charCountBits(v) + byteLength * 8;
    if (needed <= capacityBits) return v;
  }
  throw new QRError(
    `metin cok uzun: ${byteLength} bayt, surum ${MAX_VERSION} byte modunda en fazla ${dataCodewords(MAX_VERSION) - 3} bayt alir`,
  );
}

/** Veri kod sozcuklerini uretir: mod + sayac + veri + sonlandirici + dolgu. */
function encodeData(bytes: readonly number[], version: number): number[] {
  const buf = new BitBuffer();
  buf.put(0b0100, 4); // byte modu
  buf.put(bytes.length, charCountBits(version));
  for (const b of bytes) buf.put(b, 8);

  const capacity = dataCodewords(version) * 8;
  // Sonlandirici en fazla 4 bit, kalan yer kadar.
  buf.put(0, Math.min(4, capacity - buf.length));
  // Bayt sinirina hizala.
  if (buf.length % 8 !== 0) buf.put(0, 8 - (buf.length % 8));

  const words = buf.toBytes();
  // Dolgu baytlari standartta sabit ve sirayla: 0xEC, 0x11.
  const pad = [0xec, 0x11];
  for (let i = 0; words.length < dataCodewords(version); i++) {
    words.push(num(pad, i % 2));
  }
  return words;
}

/**
 * Bloklara bol, EC hesapla ve standardin istedigi sirada ic ice gecir.
 *
 * Serpistirme (interleaving) gercek bir gereklilik: hasar bir bolgede
 * yogunlastiginda tek blogu degil tum bloklari az az etkilesin diye.
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

// ----------------------------------------------------------------- matris

/** Modul durumu: null henuz yazilmamis (fonksiyon deseni degil, veri degil). */
type Grid = Array<Array<boolean | null>>;

function emptyGrid(size: number): Grid {
  return Array.from({ length: size }, () => new Array<boolean | null>(size).fill(null));
}

/** Grid'e tek modul yazar; sinir disi yazim sessizce yok sayilir. */
function gset(grid: Grid, r: number, c: number, value: boolean | null): void {
  const row = grid[r];
  if (row) row[c] = value;
}

/** Grid'den okur; sinir disi icin undefined. */
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

  // Uc bulucu desen + ayiricilari.
  placeFinder(grid, 0, 0);
  placeFinder(grid, 0, size - 7);
  placeFinder(grid, size - 7, 0);

  // Zamanlama desenleri (6. satir ve 6. kolon).
  for (let i = 8; i < size - 8; i++) {
    const dark = i % 2 === 0;
    gset(grid, 6, i, dark);
    gset(grid, i, 6, dark);
  }

  // Hizalama desenleri.
  const centers = ALIGNMENT[version - 1] ?? [];
  for (const r of centers) {
    for (const c of centers) {
      // Bulucu desenlerin uzerine gelenler atlanir.
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

  // Koyu modul.
  gset(grid, size - 8, 8, true);

  // Bicim bilgisi alanlarini rezerve et (degerleri sonra yazilir).
  reserveFormatAreas(grid);

  // Surum bilgisi alanlari (surum 7+).
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
 * Veri bitlerini zikzak yerlestirir.
 *
 * Iki modul genisliginde kolonlar, sagdan sola; her kolonda yon degisir.
 * 6. kolon zamanlama deseni oldugu icin atlanir.
 */
function placeData(grid: Grid, bits: readonly number[]): void {
  const size = grid.length;
  let bitIndex = 0;
  let upward = true;

  let right = size - 1;
  while (right >= 1) {
    // 6. kolon zamanlama deseni: kolon ciftleri oradan itibaren bir sola
    // kayar, yani sayaci da kaydirmak gerekir (yoksa sonraki ciftler
    // birbirinin uzerine biner).
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

/** Maske formulleri (ISO/IEC 18004 Tablo 10). */
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

/** Fonksiyon deseni olmayan modullere maske uygular. */
function applyMask(grid: Grid, functionMap: boolean[][], mask: number): boolean[][] {
  const size = grid.length;
  const formula = MASKS[mask];
  if (!formula) throw new QRError(`maske 0-7 arasinda olmali, verilen: ${mask}`);

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

/** BCH(15,5) bicim bilgisi; seviye M icin gostergesi 00. */
function formatBits(mask: number): number {
  const data = (0b00 << 3) | mask;
  let rem = data << 10;
  for (let i = 4; i >= 0; i--) {
    if ((rem >>> (i + 10)) & 1) rem ^= 0x537 << i;
  }
  return ((data << 10) | rem) ^ 0x5412;
}

/** BCH(18,6) surum bilgisi (surum 7+). */
function versionBits(version: number): number {
  let rem = version << 12;
  for (let i = 5; i >= 0; i--) {
    if ((rem >>> (i + 12)) & 1) rem ^= 0x1f25 << i;
  }
  return (version << 12) | rem;
}

/** Tek modul yazar; sinir disi yazim sessizce yok sayilir. */
function put(modules: boolean[][], r: number, c: number, value: boolean): void {
  const row = modules[r];
  if (row) row[c] = value;
}

function writeFormatInfo(modules: boolean[][], mask: number): void {
  const size = modules.length;
  const bits = formatBits(mask);

  for (let i = 0; i < 15; i++) {
    const on = ((bits >>> i) & 1) === 1;

    // Sol ust kose: dusuk bitler 8. KOLONDA yukaridan asagi, yuksek bitler
    // 8. SATIRDA sagdan sola (ISO/IEC 18004 Sekil 19).
    if (i < 6) put(modules, i, 8, on);
    else if (i === 6) put(modules, 7, 8, on);
    else if (i === 7) put(modules, 8, 8, on);
    else if (i === 8) put(modules, 8, 7, on);
    else put(modules, 8, 14 - i, on);

    // Kopyasi: dusuk bitler 8. satirda sagdan, yuksek bitler 8. kolonda
    // asagidan.
    if (i < 8) put(modules, 8, size - 1 - i, on);
    else put(modules, size - 15 + i, 8, on);
  }
  // Koyu modul maskelenmez.
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

// --------------------------------------------------------------- penalti

/** Maske secimi icin penalti (ISO/IEC 18004 Tablo 11). */
export function penalty(modules: readonly boolean[][]): number {
  const size = modules.length;
  let score = 0;

  /** Sinir disi okuma acik modul sayilir (kenar penaltilarini bozmasin). */
  const cell = (r: number, c: number): boolean => modules[r]?.[c] === true;

  // Kural 1: 5 ve uzeri ayni renk dizisi.
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

  // Kural 2: 2x2 ayni renk bloklar.
  for (let r = 0; r < size - 1; r++) {
    for (let c = 0; c < size - 1; c++) {
      const v = cell(r, c);
      if (cell(r, c + 1) === v && cell(r + 1, c) === v && cell(r + 1, c + 1) === v) {
        score += 3;
      }
    }
  }

  // Kural 3: bulucu desene benzeyen 1:1:3:1:1 dizisi (iki yonde).
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

  // Kural 4: koyu modul oraninin %50'den sapmasi.
  let dark = 0;
  for (const row of modules) {
    for (const v of row) if (v) dark++;
  }
  const ratio = (dark * 100) / (size * size);
  score += Math.floor(Math.abs(ratio - 50) / 5) * 10;

  return score;
}

// ----------------------------------------------------------------- girdi

/**
 * Metni QR koduna cevirir.
 *
 * @param text kodlanacak metin (UTF-8 bayta cevrilir)
 * @param opts.mask maskeyi zorlar (0-7). Verilmezse penalti kurallariyla
 *   secilir. Zorlama test vektorleri icin: ucuncu uygulama ile bayt bayt
 *   karsilastirma ancak maske sabitken anlamli olur.
 */
export function encode(text: string, opts?: { readonly mask?: number }): QRCode {
  if (text === "") throw new QRError("bos metin kodlanamaz");

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

  // Fonksiyon desenlerinin haritasi: maske yalnizca veri modullerine uygulanir.
  const functionMap = grid.map((row) => row.map((cell) => cell !== null));

  placeData(grid, bits);

  const forced = opts?.mask;
  if (forced !== undefined && (!Number.isInteger(forced) || forced < 0 || forced > 7)) {
    throw new QRError(`maske 0-7 arasinda olmali, verilen: ${forced}`);
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
  if (!best) throw new QRError("maske secilemedi");

  return { version, mask: bestMask, size, modules: best };
}
