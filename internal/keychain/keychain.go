// Package keychain keeps taw-fleet's secrets in the login keychain through
// /usr/bin/security. A secret goes in on stdin (`security -i`), never on the
// command line where other processes could see it, and is never written to a
// file.
package keychain

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/exec"
)

// Service is the keychain service every taw-fleet item is stored under.
const Service = "taw-fleet"

// ErrNotFound means the item isn't in the keychain.
var ErrNotFound = errors.New("not in the keychain")

// Item names one secret: its account under Service, and the label Keychain
// Access shows.
type Item struct {
	Account string
	Label   string
}

// Keychain reads and writes items.
type Keychain struct{ Exec exec.Runner }

// Save stores the secret, replacing an earlier one.
func (k Keychain) Save(ctx context.Context, it Item, secret string) error {
	if strings.ContainsAny(secret, "\n\r") {
		return errors.New("keychain: the secret must be one line")
	}
	cmd := fmt.Sprintf("add-generic-password -U -s %s -a %s -l %s -w %s\n", Service, it.Account, quote(it.Label), quote(secret))
	res, err := k.Exec.Run(ctx, exec.Spec{Name: "/usr/bin/security", Args: []string{"-i"}, Stdin: strings.NewReader(cmd)})
	if err != nil {
		return err
	}
	if res.Code != 0 || strings.Contains(string(res.Stderr), "rror") {
		return fmt.Errorf("keychain: %s", strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// Load reads the secret; ErrNotFound when there is none.
func (k Keychain) Load(ctx context.Context, it Item) (string, error) {
	res, err := k.Exec.Run(ctx, exec.Spec{Name: "/usr/bin/security", Args: []string{"find-generic-password", "-s", Service, "-a", it.Account, "-w"}})
	if err != nil {
		return "", err
	}
	if res.Code == 44 { // errSecItemNotFound
		return "", ErrNotFound
	}
	if res.Code != 0 {
		return "", fmt.Errorf("keychain: %s", strings.TrimSpace(string(res.Stderr)))
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// quote is a double-quoted word for security -i's parser.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
