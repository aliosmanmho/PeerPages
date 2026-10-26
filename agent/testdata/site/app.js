// e2e testi bu mutasyonlari dogruluyor: JS tunel uzerinden gelip calisti mi.
document.addEventListener("DOMContentLoaded", async () => {
  document.body.dataset.jsCalisti = "evet";

  try {
    const res = await fetch("api/items");
    if (!res.ok) throw new Error("HTTP " + res.status);
    const data = await res.json();
    const liste = document.getElementById("liste");
    liste.innerHTML = "";
    for (const item of data.items) {
      const li = document.createElement("li");
      li.textContent = item.ad + " = " + item.deger;
      li.className = "api-item";
      liste.appendChild(li);
    }
    document.body.dataset.apiCalisti = "evet";
  } catch (err) {
    document.body.dataset.apiHata = String(err);
  }
});
