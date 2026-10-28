import { message } from "./message.js";

function render(text) {
  const el = document.getElementById("message");
  if (el) el.textContent = text;
}

render(message);

// The HMR hook: when the module changes it redraws WITHOUT reloading the
// page. The test's "it did not reload" claim rests on this.
if (import.meta.hot) {
  import.meta.hot.accept("./message.js", (next) => {
    if (next) render(next.message);
  });
}
