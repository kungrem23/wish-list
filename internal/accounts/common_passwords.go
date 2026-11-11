package accounts

import (
	_ "embed"
	"strings"
)

// The first 1,000 distinct eligible entries from Dropbox zxcvbn v4.4.2.
// See common_passwords.LICENSE for the upstream license.
//
//go:embed common_passwords.txt
var passwordList string

var commonPasswords = func() map[string]struct{} {
	passwords := make(map[string]struct{})
	for password := range strings.SplitSeq(strings.TrimSuffix(passwordList, "\n"), "\n") {
		passwords[password] = struct{}{}
	}
	return passwords
}()
