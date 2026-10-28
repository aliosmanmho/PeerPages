import { CONNECT_PATH, inBase } from "./base";

/**
 * Bootloader at the scope root.
 *
 * Installs the Service Worker, then moves on to the connect screen. The
 * hash is preserved, which is how `#peer-id` travels.
 *
 * While the tunnel is open this script never runs: the SW proxies the
 * scope root to the local site (src/sw.ts).
 */
async function boot(): Promise<void> {
  const msg = document.getElementById("boot-msg");

  if ("serviceWorker" in navigator) {
    try {
      await navigator.serviceWorker.register(inBase("sw.js"), { scope: inBase("") });
    } catch (err) {
      if (msg) {
        msg.textContent = `Service Worker could not be installed: ${String(err)}`;
      }
      return;
    }
  }

  location.replace(CONNECT_PATH + location.hash);
}

void boot();
