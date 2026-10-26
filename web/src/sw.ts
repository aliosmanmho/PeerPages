/// <reference lib="webworker" />

/**
 * PeerPages Service Worker — elle yazilmis, elle derlenir
 * (README: vite-plugin-pwa kullanilmaz).
 *
 * Faz 0'da yalnizca yasam dongusu var. Fetch koprusunu Faz 1.4 ekler.
 */

declare const self: ServiceWorkerGlobalScope;

self.addEventListener("install", () => {
  // Yeni SW'yi hemen devreye al: kullanici sekmeyi kapatip acmak zorunda kalmasin.
  self.skipWaiting();
});

self.addEventListener("activate", (event: ExtendableEvent) => {
  event.waitUntil(self.clients.claim());
});

export {};
