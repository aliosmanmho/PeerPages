// The e2e test checks these mutations: did the JS arrive over the tunnel
// and run?
document.addEventListener("DOMContentLoaded", async () => {
  document.body.dataset.jsRan = "yes";

  try {
    const res = await fetch("api/items");
    if (!res.ok) throw new Error("HTTP " + res.status);
    const data = await res.json();
    const items = document.getElementById("items");
    items.innerHTML = "";
    for (const item of data.items) {
      const li = document.createElement("li");
      li.textContent = item.name + " = " + item.value;
      li.className = "api-item";
      items.appendChild(li);
    }
    document.body.dataset.apiRan = "yes";
  } catch (err) {
    document.body.dataset.apiError = String(err);
  }
});
