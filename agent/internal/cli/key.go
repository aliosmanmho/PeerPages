package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/aliosmanmho/PeerPages/agent/internal/identity"
)

// DefaultAppURL ziyaretci URL'sini basarken kullanilan varsayilan adres.
//
// Faz 7'de GERCEK dagitim adresi: app shell bu adreste yayinda
// (.github/workflows/pages.yml). Onceki sablon deger ("<kullanici>")
// bayraksiz calisan ajanin CALISMAYAN bir URL basmasina yol aciyordu --
// "yabanci biri 3 dakikada paylasabiliyor" kriterinin en kritik tek satiri.
//
// Kendi kopyasini yayinlayan ya da custom domain kullanan --app-url verir.
const DefaultAppURL = "https://aliosmanmho.github.io/PeerPages/"

func newKeyCmd() *cobra.Command {
	var (
		keyPath    string
		appURL     string
		targetName string
		create     bool
		reveal     bool
	)

	cmd := &cobra.Command{
		Use:   "key",
		Short: "Ajan kimligini (peer-id) goster ya da olustur",
		Long: `Ajanin Nostr kimligini yonetir.

Anahtar ~/.peerpages/key dosyasinda nsec (bech32) olarak tutulur. peer-id
ziyaretci URL'sinde '#' sonrasina konan TAM npub'dir.

ONEMLI: bu anahtari kaybedersen peer-id'n degisir, yani paylastigin URL
calismaz. Dosyayi yedekle ve kimseyle paylasma.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := resolveKeyPath(keyPath)
			if err != nil {
				return err
			}

			var id identity.Identity
			if create {
				id, err = identity.Generate()
				if err != nil {
					return err
				}
				// Save var olan dosyanin uzerine yazmaz; peer-id sessizce
				// degismesin diye bilerek boyle.
				if err := identity.Save(path, id); err != nil {
					return fmt.Errorf("%w\n\nVar olan anahtari degistirmek istiyorsan dosyayi once kendin tasi: %s", err, path)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "yeni anahtar olusturuldu: %s\n\n", path)
			} else {
				var created bool
				id, created, err = identity.LoadOrCreate(path)
				if err != nil {
					return err
				}
				if created {
					fmt.Fprintf(cmd.OutOrStdout(), "anahtar yoktu, olusturuldu: %s\n\n", path)
				}
			}

			return printIdentity(cmd, id, path, appURL, targetName, reveal)
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&keyPath, "path", "", "anahtar dosyasi (varsayilan: ~/.peerpages/key)")
	fl.StringVar(&appURL, "app-url", DefaultAppURL, "app shell'in yayinlandigi adres")
	fl.StringVar(&targetName, "target", "",
		"ziyaretci URL'sine eklenecek hedef adi (bos ya da 'default' ise eklenmez)")
	fl.BoolVar(&create, "new", false, "yeni anahtar olustur (var olan dosyanin uzerine yazmaz)")
	fl.BoolVar(&reveal, "reveal-secret", false,
		"GIZLI ANAHTARI ekrana yazdir — yalnizca yedeklemek icin, dikkatli ol")
	return cmd
}

func resolveKeyPath(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	return identity.DefaultKeyPath()
}

func printIdentity(
	cmd *cobra.Command, id identity.Identity, path, appURL, targetName string, reveal bool,
) error {
	out := cmd.OutOrStdout()

	npub, err := id.Npub()
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "anahtar dosyasi : %s\n", path)
	fmt.Fprintf(out, "peer-id (npub)  : %s\n", npub)
	fmt.Fprintf(out, "kisa gosterim   : %s\n", identity.ShortenNpub(npub))
	fmt.Fprintf(out, "pubkey (hex)    : %s\n", id.PublicKey)

	url, err := id.VisitorURL(appURL, "", targetName)
	if err == nil {
		fmt.Fprintf(out, "\nziyaretci URL'si:\n  %s\n", url)
	}

	if reveal {
		nsec, err := id.Nsec()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "\nGIZLI ANAHTAR (kimseyle paylasma):\n  %s\n", nsec)
	} else {
		fmt.Fprintf(out, "\nGizli anahtari gormek icin: --reveal-secret\n")
	}

	// Dosya izinleri Windows'ta POSIX'e gore degil; kullaniciyi uyar.
	if st, serr := os.Stat(path); serr == nil && st.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"\nuyari: %s baskalari tarafindan okunabilir gorunuyor (izin %o)\n",
			path, st.Mode().Perm())
	}
	return nil
}
