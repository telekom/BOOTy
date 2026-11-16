package rescue

import (
	"strings"
	"testing"
)

// Hashes made by other tools: openssl passwd -6/-5/-1, htpasswd -B and
// libxcrypt (the yescrypt example of yescrypt-go).
func TestCheckPassword(t *testing.T) {
	bcrypt := "$2y$05$wnOHkDYpcRIikWnQOmg51ugpUl2dRC3iXg5u5dlvX4zQO/dlMZJqq"
	for _, tc := range []struct{ name, hash, password string }{
		{"yescrypt", "$y$j9T$AAt9R641xPvCI9nXw1HHW/$cuQRBMN3N/f8IcmVN.4YrZ1bHMOiLOoz9/XQMKV/v0A", "openwall"},
		{"sha512crypt", "$6$testsalt$xS1o3e00o4ssyGlOrd8nk2fjZUsB1BdteI4yWVWI4UCNzmA3S0y628NkbLH31AgcUBLoyxJRoZSCpakWLAixK0", "rescue-pw"},
		{"sha256crypt", "$5$testsalt$CVA1Dnt8WToxgKpCoGIpFhjfzIYe1FzUhDDbmcZSh69", "rescue-pw"},
		{"md5crypt", "$1$testsalt$zWhQMOhvw88GqpvkxC4YC1", "rescue-pw"},
		{"bcrypt 2y", bcrypt, "rescue-pw"},
		{"bcrypt 2b", strings.Replace(bcrypt, "$2y$", "$2b$", 1), "rescue-pw"},
		{"bcrypt 2a", strings.Replace(bcrypt, "$2y$", "$2a$", 1), "rescue-pw"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !checkPassword(tc.hash, tc.password) {
				t.Error("the right password is refused")
			}
			for _, wrong := range []string{"", "wrong", tc.password + "x"} {
				if checkPassword(tc.hash, wrong) {
					t.Errorf("password %q is accepted", wrong)
				}
			}
		})
	}
	for _, hash := range []string{"", "plain", "$7$scrypt", "$6$x:y$z"} {
		if checkPassword(hash, "plain") {
			t.Errorf("hash %q accepts a password", hash)
		}
	}
}
