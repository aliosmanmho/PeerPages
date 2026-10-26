import { CONNECT_PATH, inBase } from "./base";

/**
 * Scope kokundeki onyukleyici.
 *
 * Service Worker'i kurar, sonra baglanti ekranina gecer. Hash korunur:
 * `#peer-id` Faz 2'de burada tasinacak.
 *
 * Tunel acikken bu betik hic calismaz; SW scope kokunu yerel siteye
 * proxy'ler (src/sw.ts).
 */
async function boot(): Promise<void> {
  const msg = document.getElementById("boot-msg");

  if ("serviceWorker" in navigator) {
    try {
      await navigator.serviceWorker.register(inBase("sw.js"), { scope: inBase("") });
    } catch (err) {
      if (msg) {
        msg.textContent = `Service Worker kurulamadi: ${String(err)}`;
      }
      return;
    }
  }

  location.replace(CONNECT_PATH + location.hash);
}

void boot();
