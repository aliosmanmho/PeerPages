/**
 * Tunelin kendi kendine toparlanmasi (BRIEF Faz 4).
 *
 * Neden ziyaretci tarafi: ajan yalnizca CEVAP veren taraftir (answerer-only)
 * ve oturum tek seferliktir — yeni bir baglanti ancak ziyaretcinin yeni bir
 * offer yayinlamasiyla kurulur. Ustelik Faz 3'un bosta kalma denetimi 30
 * saniye sessizlikten sonra ajan tarafindaki oturumu zaten kapatiyor, yani
 * uyku/uyanma gibi uzun kopmalarda kurtarilacak bir oturum kalmiyor:
 * her deneme SIFIRDAN yeni oturum kurar (yeni sid, yeni gecici keypair).
 *
 * Bu dosya DOM'dan ve signaling'den bagimsiz tutuldu: denemeyi yapan
 * fonksiyon disaridan veriliyor, boylece durum makinesi tek basina test
 * edilebiliyor.
 */

export type SupervisorState = "connected" | "reconnecting" | "idle";

/** Varsayilan geri sayim (ms). Son deger tekrar eder. */
export const DEFAULT_BACKOFF_MS = [1000, 2000, 4000, 8000, 15000, 30000] as const;

export interface SupervisorInfo {
  /** Kacinci deneme (1'den baslar; baglandiginda 0). */
  readonly attempt: number;
  /** Bir sonraki denemeye kalan sure (ms); deneme suruyorsa 0. */
  readonly waitMs: number;
  /** Son kopma ya da basarisizlik nedeni. */
  readonly reason?: string;
}

export interface SupervisorOptions {
  /**
   * Tek bir baglanma denemesi. Basarili donerse tunel acik kabul edilir;
   * hata atarsa geri sayimla yeniden denenir.
   */
  readonly connect: () => Promise<void>;
  /** Durum degisimlerini bildirir (ekran icin). */
  readonly onState?: (state: SupervisorState, info: SupervisorInfo) => void;
  /** Geri sayim listesi; verilmezse DEFAULT_BACKOFF_MS. */
  readonly backoffMs?: readonly number[];
  /**
   * Olay kaynagi; verilmezse `window`. Testler sahte bir hedef verebilir.
   * `online` ve `visibilitychange` olaylari geri sayimi SIFIRLAR: ikisi de
   * "ag ya da cihaz geri geldi" demektir ve uyku/uyanma yolu tam olarak
   * budur.
   */
  readonly events?: Pick<EventTarget, "addEventListener" | "removeEventListener">;
  /** Sekmenin gorunur olup olmadigi; verilmezse document.visibilityState. */
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
    // Ag ya da sekme geri geldi: beklemeyi kes, hemen dene.
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

  /** Olaylari dinlemeye baslar. */
  start(): void {
    if (this.running) return;
    this.running = true;
    this.events?.addEventListener("online", this.onWake);
    this.events?.addEventListener("visibilitychange", this.onWake);
  }

  /** Dinlemeyi birakir ve bekleyen denemeyi iptal eder. */
  stop(): void {
    this.running = false;
    this.clearTimer();
    this.events?.removeEventListener("online", this.onWake);
    this.events?.removeEventListener("visibilitychange", this.onWake);
    this.state = "idle";
  }

  /** Tunel acildi (ilk baglanti ya da yeniden baglanma). */
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
   * Tunel koptu: geri sayimla yeniden denemeye baslar.
   *
   * Ayni kopma icin birden cok kaynak haber verebilir (WebRTC durumu ve
   * canlilik ping'i), bu yuzden tekrarlanan cagrilar geri sayimi
   * uzatmaz.
   */
  markLost(reason: string): void {
    if (!this.running) return;
    if (this.state === "reconnecting") return;
    this.state = "reconnecting";
    this.lastReason = reason;
    this.attempt = 0;
    this.schedule();
  }

  /** Beklemeden hemen dene (kullanici "simdi dene" dedi). */
  retryNow(): void {
    if (!this.running) return;
    this.clearTimer();
    this.state = "reconnecting";
    void this.tryOnce();
  }

  get currentState(): SupervisorState {
    return this.state;
  }

  /** Kac kez yeniden baglanildi. */
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
      // Basari markConnected ile bildirilir: tunelin gercekten acildigina
      // karar veren yer cagiran taraf.
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
