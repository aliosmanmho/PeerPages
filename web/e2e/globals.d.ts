/**
 * E2E testinin sayfadan okudugu pencere kancasi (src/main.ts sonunda
 * tanimlaniyor). Yalnizca test icin; uretim davranisini etkilemez.
 */
declare global {
  interface Window {
    __peerpages?: {
      hasTunnel: () => boolean;
      stats: () => unknown;
      ping: () => Promise<number>;
      makeOffer: () => Promise<void>;
      offer: () => string;
      acceptAnswer: (pasted: string) => Promise<void>;
    };
  }
}

export {};
