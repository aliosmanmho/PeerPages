import "./style.css";
import { BASE_PATH, inBase } from "./base";
import { StatusView } from "./ui/status";

const status = new StatusView();

async function registerServiceWorker(): Promise<ServiceWorkerRegistration | null> {
  if (!("serviceWorker" in navigator)) {
    status.step("sw", "error");
    status.say("Bu tarayici Service Worker desteklemiyor — tunel kurulamaz.", "error");
    return null;
  }

  status.step("sw", "active");
  status.say("Service Worker kuruluyor…");

  try {
    // Scope BASE_PATH ile sinirli: SW yalnizca kendi kapsamindaki istekleri
    // yakalar, baska origin'lere ya da ust yollara dokunmaz (ilke 5).
    const reg = await navigator.serviceWorker.register(inBase("sw.js"), {
      scope: BASE_PATH,
    });
    await navigator.serviceWorker.ready;
    status.step("sw", "done");
    status.say("Service Worker aktif.");
    return reg;
  } catch (err) {
    status.step("sw", "error");
    status.say(`Service Worker kaydi basarisiz: ${String(err)}`, "error");
    return null;
  }
}

async function main(): Promise<void> {
  const reg = await registerServiceWorker();
  if (!reg) return;

  status.step("signal", "active");
  status.say("Ajanin offer'ini yapistir.");
}

void main();
