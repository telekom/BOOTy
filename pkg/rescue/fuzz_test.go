package rescue

import (
	"strings"
	"testing"
)

// FuzzParseAuthorizedKey feeds RESCUE_SSH_PUBKEY values through both
// parsers: neither may panic, and an entry that the server would use
// carries only the options it understands.
func FuzzParseAuthorizedKey(f *testing.F) {
	for _, s := range []string{
		testKeyA,
		"restrict " + testKeyA,
		`command="echo \"hi\"; id",no-pty ` + testKeyA + " comment",
		`permitopen="[::1]:22",permitopen="db:*" ` + testKeyA,
		`permitopen="host:99999" ` + testKeyA,
		testKeyA + ";" + testKeyA,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, value string) {
		keys, _ := ParseSSHKeys(value)
		for _, e := range keys {
			k, err := parseAuthorizedKey(e)
			if err != nil {
				continue
			}
			for _, p := range k.opts.permitOpen {
				if p.host == "" || p.port < -1 || p.port > 65535 {
					t.Fatalf("permitopen %+v from %q", p, e)
				}
			}
			if k.opts.hasCommand && strings.ContainsAny(k.opts.command, "\n\r") {
				t.Fatalf("command with a line break from %q", e)
			}
		}
	})
}

// FuzzCheckPassword checks that no hash, valid or not, panics or accepts
// an empty password.
func FuzzCheckPassword(f *testing.F) {
	for _, s := range []string{
		"$y$j9T$AAt9R641xPvCI9nXw1HHW/$cuQRBMN3N/f8IcmVN.4YrZ1bHMOiLOoz9/XQMKV/v0A",
		testHashFuzz,
		"$5$testsalt$CVA1Dnt8WToxgKpCoGIpFhjfzIYe1FzUhDDbmcZSh69",
		"$1$testsalt$zWhQMOhvw88GqpvkxC4YC1",
		"$2y$05$wnOHkDYpcRIikWnQOmg51ugpUl2dRC3iXg5u5dlvX4zQO/dlMZJqq",
		"$6$rounds=1000$x$", "$y$", "$2b$", "$1$$",
	} {
		f.Add(s, "rescue-pw")
	}
	f.Fuzz(func(t *testing.T, hash, password string) {
		// Expensive cost parameters only make the fuzzer slow (the hash is
		// trusted config): keep sha-crypt rounds, the bcrypt cost and the
		// yescrypt parameters at the seed values.
		switch {
		case strings.Contains(hash, "rounds="),
			strings.HasPrefix(hash, "$2") && (len(hash) < 7 || hash[4:6] > "05"),
			strings.HasPrefix(hash, "$y$") && !strings.HasPrefix(hash, "$y$j9T$"):
			return
		}
		if checkPassword(hash, "") {
			t.Fatalf("hash %q accepts the empty password", hash)
		}
		_ = checkPassword(hash, password)
	})
}

const testHashFuzz = "$6$testsalt$xS1o3e00o4ssyGlOrd8nk2fjZUsB1BdteI4yWVWI4UCNzmA3S0y628NkbLH31AgcUBLoyxJRoZSCpakWLAixK0"
