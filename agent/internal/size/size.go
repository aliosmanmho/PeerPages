// Package size insan tarafindan yazilan boyut ifadelerini bayta cevirir.
package size

import (
	"fmt"
	"strconv"
	"strings"
)

// Birimler 1024 tabanlidir: 1MB = 1MiB = 1048576 bayt. CLI'da bir ust sinir
// belirtirken beklenen davranis bu; "50MB" yazan kullanici 50*1024*1024 ister.
var units = []struct {
	suffix string
	mult   int64
}{
	{"GIB", 1 << 30},
	{"MIB", 1 << 20},
	{"KIB", 1 << 10},
	{"GB", 1 << 30},
	{"MB", 1 << 20},
	{"KB", 1 << 10},
	{"G", 1 << 30},
	{"M", 1 << 20},
	{"K", 1 << 10},
	{"B", 1},
}

// Parse "50MB", "512KiB", "1073741824" gibi ifadeleri bayta cevirir.
// Negatif ve sifir degerler hatadir: sinir her zaman pozitif olmali.
func Parse(s string) (int64, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return 0, fmt.Errorf("bos boyut ifadesi")
	}
	upper := strings.ToUpper(raw)

	for _, u := range units {
		if !strings.HasSuffix(upper, u.suffix) {
			continue
		}
		numPart := strings.TrimSpace(upper[:len(upper)-len(u.suffix)])
		if numPart == "" {
			return 0, fmt.Errorf("boyut ifadesinde sayi yok: %q", raw)
		}
		n, err := strconv.ParseInt(numPart, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("gecersiz boyut ifadesi %q: sayi okunamadi", raw)
		}
		if n <= 0 {
			return 0, fmt.Errorf("boyut pozitif olmali: %q", raw)
		}
		if n > (1<<62)/u.mult {
			return 0, fmt.Errorf("boyut cok buyuk: %q", raw)
		}
		return n * u.mult, nil
	}

	n, err := strconv.ParseInt(upper, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("gecersiz boyut ifadesi %q", raw)
	}
	if n <= 0 {
		return 0, fmt.Errorf("boyut pozitif olmali: %q", raw)
	}
	return n, nil
}

// Format bayti okunur hale getirir (loglar ve --help icin).
func Format(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.3gGB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.3gMB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.3gKB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}
