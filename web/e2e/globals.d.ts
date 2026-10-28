/**
 * E2E testinin sayfadan okudugu pencere kancasi (src/main.ts sonunda
 * declared). For the tests only; it does not affect production behaviour.
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
      /** The supervisor's state: connected | reconnecting | idle. */
      connState: () => string;
      /** The connection route: host | srflx | prflx | relay | unknown (phase 5). */
      iceRoute: () => string;
      /** The number of open WebSocket sessions (phase 6). */
      wsSessions: () => number;
    };
  }
}

export {};
