// Command gen Faz 1 test sitesi icin uretilen dosyalari olusturur.
//
// Su an yalnizca 5 MB'lik big.bin: repoda tutulmuyor, deterministik olarak
// uretiliyor. `make testdata` ve scripts/*.ps1 bunu cagirir.
//
// Kullanim:
//
//	go run ./internal/testsite/gen            # agent/testdata/site icine
//	go run ./internal/testsite/gen -dir yol   # baska bir dizine
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/aliosmanmho/PeerPages/agent/internal/testsite"
)

func main() {
	dir := flag.String("dir", "testdata/site", "test sitesi dizini")
	printHash := flag.Bool("sha256", false, "big.bin'in beklenen sha256'sini yazdir")
	flag.Parse()

	if *printHash {
		fmt.Println(testsite.BigFileSHA256())
		return
	}

	path, created, err := testsite.GenerateBig(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "big.bin uretilemedi: %v\n", err)
		os.Exit(1)
	}
	if created {
		fmt.Printf("uretildi: %s (%d bayt, sha256=%s)\n", path, testsite.BigFileSize, testsite.BigFileSHA256())
		return
	}
	fmt.Printf("zaten var: %s (%d bayt)\n", path, testsite.BigFileSize)
}
