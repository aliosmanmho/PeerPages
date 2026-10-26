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
      acceptOffer: (pasted: string) => Promise<void>;
      answer: () => string;
    };
  }
}

export {};
