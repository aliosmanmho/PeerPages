import { mesaj } from "./mesaj.js";

function render(text) {
  const el = document.getElementById("mesaj");
  if (el) el.textContent = text;
}

render(mesaj);

// HMR kancasi: modul degisince sayfayi YENILEMEDEN yeniden cizer. Testin
// "yenilenmedi" iddiasi buna dayaniyor.
if (import.meta.hot) {
  import.meta.hot.accept("./mesaj.js", (yeni) => {
    if (yeni) render(yeni.mesaj);
  });
}
