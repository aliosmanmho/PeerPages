/**
 * E2E testinin sayfadan okudugu pencere kancasi (src/main.ts sonunda
 * tanimlaniyor). Yalnizca test icin; uretim davranisini etkilemez.
 */
declare global {
  interface Window {
    __peerpages?: {
      hasTunnel: () => boolean;
      stats: () => {
        requests: number;
        errors: number;
        bytes: number;
        tunnel: {
          requests: number;
          bytesIn: number;
          bytesOut: number;
          inflight: number;
        } | null;
      };
      ping: () => Promise<number>;
      makeOffer: () => Promise<void>;
      offer: () => string;
      acceptAnswer: (pasted: string) => Promise<void>;
      /** Kac kez yeniden baglanildi (Faz 4). */
      reconnects: () => number;
      /** Denetleyicinin durumu: connected | reconnecting | idle. */
      connState: () => string;
      /** Baglanti yolu: host | srflx | prflx | relay | bilinmiyor (Faz 5). */
      iceRoute: () => string;
      /** Acik WebSocket oturumu sayisi (Faz 6). */
      wsSessions: () => number;
    };
  }
}

export {};
