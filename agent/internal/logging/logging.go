// Package logging ajanin yapilandirilmis loglamasini kurar.
//
// BRIEF guvenlik ilkesi 6: "Varsayilan loglarda URL path'leri maskelenir
// (--verbose ile acilir)." Bu paket maskelemeyi tek yerde tutar; proxy ve CLI
// katmanlari dogrudan ham path loglamaz.
package logging

import (
	"io"
	"log/slog"
	"strconv"
	"strings"
)

// New verbose bayragina gore bir logger uretir.
// verbose: debug seviyesi acilir ve path'ler maskelenmez.
func New(w io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

// MaskPath bir istek yolunu loglanabilir hale getirir.
//
// verbose true ise yol oldugu gibi doner. Degilse her segment uzunluguna
// indirgenir ve query tamamen gizlenir:
//
//	/api/items?x=1  ->  /*3/*5?*3
//	/               ->  /
//
// Boylece log yapiyi (kac segment, ne kadar uzun) korur ama icerigi sizdirmaz.
func MaskPath(p string, verbose bool) string {
	if verbose {
		return p
	}

	path, query, hasQuery := strings.Cut(p, "?")

	var b strings.Builder
	if path == "" {
		b.WriteString("/")
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "" {
			continue
		}
		b.WriteString("/*")
		b.WriteString(strconv.Itoa(len(seg)))
	}
	if b.Len() == 0 {
		b.WriteString("/")
	}
	if hasQuery {
		b.WriteString("?*")
		b.WriteString(strconv.Itoa(len(query)))
	}
	return b.String()
}

// PathAttr maskeleme kuralini uygulayan bir slog niteligi uretir.
func PathAttr(p string, verbose bool) slog.Attr {
	return slog.String("path", MaskPath(p, verbose))
}
