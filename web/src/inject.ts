/**
 * HTML akisina `<script>` sokan donustürücu (Faz 6).
 *
 * Ajan `--inject-ws-shim` ile calisirken HTML cevaplarina
 * `x-peerpages-ws-shim: 1` basligini ekler; govdeye DOKUNMAZ. Enjeksiyonu
 * Service Worker yapar, cunku scope onekini (`/PeerPages/`) bilen taraf o ve
 * boylece `internal/proxy` bayt bayt gecirgen kalir (README §8).
 *
 * Cozmesi gereken uc sey:
 *  1. `<head>` etiketi iki cerceveye bolunmus olabilir → sinirli tampon,
 *  2. `<head>` olmayabilir → `<body>`'den once, o da yoksa en basa,
 *  3. kodlama bilinmiyor → yalnizca ASCII bayt yaziyoruz, bu her
 *     ASCII-ustu kodlamada (UTF-8, latin-1, windows-1254) gecerli.
 */

/**
 * Enjeksiyon noktasi aranirken tamponlanacak en fazla bayt.
 *
 * Ust sinir ZORUNLU: `<head>` hic gelmezse akis bu tamponda beklerdi ve
 * sayfa yuklenmiyor gibi gorunurdu. Sinir dolunca script en basa yazilir.
 */
export const INJECT_SCAN_LIMIT = 8 * 1024;

/** Enjeksiyon noktasini arayan durum makinesi. */
export interface InjectPlan {
  /** Script'in girecegi bayt indeksi. */
  readonly offset: number;
  /** Karar kesinlesti mi (daha fazla bayt beklemeye gerek yok). */
  readonly decided: boolean;
}

const HEAD_OPEN = "<head";
const BODY_OPEN = "<body";

/**
 * Tamponun basindan enjeksiyon noktasini bulur.
 *
 * `<head ...>` bulunursa onun KAPANIS acisindan hemen sonrasi; yoksa
 * `<body`'nin basi; ikisi de yoksa karar verilemez (daha fazla bayt gerekir)
 * ya da sinir dolduysa 0.
 *
 * ASCII kucuk/buyuk harf duyarsiz; etiket adi disinda hicbir ayristirma
 * yapilmiyor (HTML ayristiricisi yazmak bu isin gerektirdiginden cok daha
 * fazlasi olurdu).
 */
export function planInjection(text: string, atEnd: boolean): InjectPlan {
  const lower = text.toLowerCase();

  const head = lower.indexOf(HEAD_OPEN);
  if (head >= 0) {
    const close = lower.indexOf(">", head);
    if (close >= 0) return { offset: close + 1, decided: true };
    // `<head` var ama `>` henuz gelmemis: bekle.
    return { offset: 0, decided: false };
  }

  const body = lower.indexOf(BODY_OPEN);
  if (body >= 0) {
    // `<head` yok ama `<body` var: script'i body'den once koy.
    return { offset: body, decided: true };
  }

  if (atEnd || text.length >= INJECT_SCAN_LIMIT) {
    // Ne head ne body: en basa koy. Bozuk ya da parcali HTML'de de calisir.
    return { offset: 0, decided: true };
  }
  return { offset: 0, decided: false };
}

/**
 * HTML akisina verilen script etiketini sokan `TransformStream` uretir.
 *
 * `src` zaten mutlak ya da scope'a goreli bir yol olmali; burada
 * degistirilmez.
 */
export function injectScriptStream(
  src: string,
): TransformStream<Uint8Array<ArrayBuffer>, Uint8Array<ArrayBuffer>> {
  const tag: Uint8Array<ArrayBuffer> = new TextEncoder().encode(
    // `<head>`'den hemen sonra ve ERTELENMEDEN: sayfanin kendi
    // script'lerinden once kosmali, yoksa WebSocket'i onlar once alir.
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

    // Yalnizca ASCII etiket adlari ariyoruz; bozuk cok baytli dizi sonuc
    // degistirmez.
    const text = decoder.decode(buffer, { stream: !atEnd });
    const plan = planInjection(text, atEnd);
    if (!plan.decided) return;

    // ASCII arama yaptigimiz icin metin indeksi = bayt indeksi DEGIL:
    // cok baytli karakterler kaydirir. Bu yuzden ayni arama bayt
    // duzeyinde tekrarlanir.
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
        // Karar verilemedi (bos ya da cok kisa govde): dokunmadan gecir.
        controller.enqueue(buffer);
      }
    },
  });
}

/**
 * Metin indeksini bayt indeksine cevirir.
 *
 * ASCII govdelerde ikisi aynidir; cok baytli karakter varsa metnin o
 * noktaya kadarki UTF-8 uzunlugu kullanilir.
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
