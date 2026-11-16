//go:build !linux

package rescue

import (
	"context"
	"errors"

	"golang.org/x/crypto/ssh"
)

// certVerifier is the type of the opkssh check; the server is Linux-only.
type certVerifier func(ctx context.Context, user string, cert *ssh.Certificate) (issuer, subject string, err error)

func startRescueSSH(context.Context, *Config) error {
	return errors.New("the rescue SSH server needs Linux")
}
