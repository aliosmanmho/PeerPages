package signaling_test

import (
	"testing"

	"github.com/aliosmanmho/PeerPages/agent/internal/signaling"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
)

// TestTargetNameRulesMatchTargetPackage iki ad dogrulayicisinin ayni kumeyi
// kabul ettigini gosterir.
//
// Neden ayri iki dogrulayici var: signaling paketi yalnizca mesaj bicimini
// bilir (protokolun bir parcasi), target paketi ise CLI'dan gelen hedefleri
// cozer. Birbirlerini import etmiyorlar ki katmanlar karismasin — ama
// kurallari ayrismaya baslarsa URL'den gelen bir ad signaling'i gecip
// target'ta patlar (ya da tersi). Bu test tam o kaymayi yakalar.
func TestTargetNameRulesMatchTargetPackage(t *testing.T) {
	names := []string{
		"default", "admin", "a", "A", "0", "web-2_eski",
		"a-b-c", "ABC_123",
		"a123456789012345678901234567890b",  // 32: tam sinir
		"a1234567890123456789012345678901b", // 33: asiyor
		"", "-admin", "_admin", "a/b", "a.b", "a b", "tr-ç",
	}

	for _, name := range names {
		// target.Parse'in kabulu: "ad=url" bicimi gecerli bir Set uretiyor mu.
		_, err := target.Parse([]string{name + "=http://localhost:3000"})
		targetOK := err == nil
		signalingOK := signaling.ValidTargetName(name)

		if targetOK != signalingOK {
			t.Errorf("%q: target kabul=%v, signaling kabul=%v (kurallar ayrismis)",
				name, targetOK, signalingOK)
		}
	}
}

// TestTargetNameNotTrimmed signaling tarafinin ad etrafindaki bosluklari
// KIRPMADIGINI gosterir.
//
// target paketi CLI'dan gelen `--target " admin "=url` icin kirpma yapiyor
// (kullaniciya kolaylik). Tel uzerinde ise kirpma yok: aksi halde "admin" ve
// " admin" ayni hedefe cozulur ve kanonik bicim belirsizlesir.
func TestTargetNameNotTrimmed(t *testing.T) {
	for _, name := range []string{" admin", "admin ", "\tadmin", "admin\n"} {
		if signaling.ValidTargetName(name) {
			t.Errorf("%q kabul edildi; tel uzerinde bosluk kirpilmamali", name)
		}
	}
}
