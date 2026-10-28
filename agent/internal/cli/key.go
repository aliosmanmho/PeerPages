package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/aliosmanmho/PeerPages/agent/internal/identity"
)

// DefaultAppURL is the default address used when printing the visitor
// URL.
//
// In phase 7 this became the REAL deployment address: the app shell is
// live there (.github/workflows/pages.yml). The previous placeholder
// ("<user>") made an agent run without the flag print a URL that DID NOT
// WORK — the single most critical line of the "a stranger can share in 3
// minutes" criterion.
//
// Anyone publishing their own copy, or using a custom domain, passes
// --app-url.
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
		Short: "Show or create the agent identity (peer-id)",
		Long: `Manages the agent's Nostr identity.

The key is stored in ~/.peerpages/key as an nsec (bech32). The peer-id is
the FULL npub put after '#' in the visitor URL.

IMPORTANT: if you lose this key your peer-id changes, so the URL you
shared stops working. Back the file up and share it with nobody.`,
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
				// Save does not overwrite an existing file; deliberately
				// so, to keep the peer-id from changing silently.
				if err := identity.Save(path, id); err != nil {
					return fmt.Errorf("%w\n\nTo replace the existing key, move the file yourself first: %s", err, path)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "a new key was created: %s\n\n", path)
			} else {
				var created bool
				id, created, err = identity.LoadOrCreate(path)
				if err != nil {
					return err
				}
				if created {
					fmt.Fprintf(cmd.OutOrStdout(), "there was no key, one was created: %s\n\n", path)
				}
			}

			return printIdentity(cmd, id, path, appURL, targetName, reveal)
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&keyPath, "path", "", "the key file (default: ~/.peerpages/key)")
	fl.StringVar(&appURL, "app-url", DefaultAppURL, "the address the app shell is published at")
	fl.StringVar(&targetName, "target", "",
		"the target name to add to the visitor URL (not added when empty or 'default')")
	fl.BoolVar(&create, "new", false, "create a new key (it does not overwrite an existing file)")
	fl.BoolVar(&reveal, "reveal-secret", false,
		"print the SECRET KEY to the screen — for backups only, be careful")
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

	fmt.Fprintf(out, "key file       : %s\n", path)
	fmt.Fprintf(out, "peer-id (npub) : %s\n", npub)
	fmt.Fprintf(out, "short form     : %s\n", identity.ShortenNpub(npub))
	fmt.Fprintf(out, "pubkey (hex)   : %s\n", id.PublicKey)

	url, err := id.VisitorURL(appURL, "", targetName)
	if err == nil {
		fmt.Fprintf(out, "\nvisitor URL:\n  %s\n", url)
	}

	if reveal {
		nsec, err := id.Nsec()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "\nSECRET KEY (share it with nobody):\n  %s\n", nsec)
	} else {
		fmt.Fprintf(out, "\nTo see the secret key: --reveal-secret\n")
	}

	// File permissions do not follow POSIX on Windows; warn the user.
	if st, serr := os.Stat(path); serr == nil && st.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"\nwarning: %s looks readable by others (permissions %o)\n",
			path, st.Mode().Perm())
	}
	return nil
}
