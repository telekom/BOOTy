package rescue

import (
	"crypto/subtle"
	"strings"

	"github.com/GehirnInc/crypt/md5_crypt"
	"github.com/GehirnInc/crypt/sha256_crypt"
	"github.com/GehirnInc/crypt/sha512_crypt"
	"github.com/openwall/yescrypt-go"
	"golang.org/x/crypto/bcrypt"
)

// checkPassword reports whether password matches the crypt(3) hash, for the
// schemes of supportedPasswordHashPrefixes, in pure Go: yescrypt ($y$),
// sha512crypt ($6$), sha256crypt ($5$), bcrypt ($2a$, $2b$, $2y$) and
// md5crypt ($1$). An empty password never matches, like dropbear without -B.
func checkPassword(hash, password string) bool {
	if password == "" || validatePasswordHash(hash) != nil {
		return false
	}
	pw := []byte(password)
	switch {
	case strings.HasPrefix(hash, "$y$"):
		got, err := yescrypt.Hash(pw, []byte(hash))
		return err == nil && subtle.ConstantTimeCompare(got, []byte(hash)) == 1
	case strings.HasPrefix(hash, "$6$"):
		return sha512_crypt.New().Verify(hash, pw) == nil
	case strings.HasPrefix(hash, "$5$"):
		return sha256_crypt.New().Verify(hash, pw) == nil
	case strings.HasPrefix(hash, "$1$"):
		return md5_crypt.New().Verify(hash, pw) == nil
	case strings.HasPrefix(hash, "$2"):
		return bcrypt.CompareHashAndPassword([]byte(hash), pw) == nil
	}
	return false
}
