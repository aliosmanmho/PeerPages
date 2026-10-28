// Command gen creates the generated files for the phase 1 test site.
//
// Today that is only the 5 MB big.bin: it is not kept in the repo and is
// generated deterministically. `make testdata` and scripts/*.ps1 call
// this.
//
// Usage:
//
//	go run ./internal/testsite/gen            # into agent/testdata/site
//	go run ./internal/testsite/gen -dir path  # into another directory
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/aliosmanmho/PeerPages/agent/internal/testsite"
)

func main() {
	dir := flag.String("dir", "testdata/site", "the test site directory")
	printHash := flag.Bool("sha256", false, "print the expected sha256 of big.bin")
	flag.Parse()

	if *printHash {
		fmt.Println(testsite.BigFileSHA256())
		return
	}

	path, created, err := testsite.GenerateBig(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "big.bin could not be generated: %v\n", err)
		os.Exit(1)
	}
	if created {
		fmt.Printf("generated: %s (%d bytes, sha256=%s)\n", path, testsite.BigFileSize, testsite.BigFileSHA256())
		return
	}
	fmt.Printf("already there: %s (%d bytes)\n", path, testsite.BigFileSize)
}
