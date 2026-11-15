// Package opktest is a mock OpenID Connect provider that issues opkssh PK
// Token SSH certificates, for the rescue SSH tests. It is not part of the
// BOOTy binary.
package opktest

import (
	"context"
	"crypto"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/openpubkey/openpubkey/client"
	"github.com/openpubkey/openpubkey/providers"
	"github.com/openpubkey/openpubkey/providers/mocks"
	"github.com/openpubkey/opkssh/sshcert"
	"golang.org/x/crypto/ssh"
)

// Provider is a mock OpenID provider of one issuer and client ID.
type Provider struct {
	Issuer, ClientID string
	op               *providers.MockProvider
	backend          *mocks.MockProviderBackend
	template         *mocks.IDTokenTemplate
}

// NewProvider returns a provider with new signing keys.
func NewProvider(issuer, clientID string) (*Provider, error) {
	opts := providers.DefaultMockProviderOpts()
	opts.Issuer, opts.ClientID, opts.VerifierOpts.ClientID = issuer, clientID, clientID
	op, backend, template, err := providers.NewMockProvider(opts)
	if err != nil {
		return nil, fmt.Errorf("mock provider: %w", err)
	}
	return &Provider{Issuer: issuer, ClientID: clientID, op: op, backend: backend, template: template}, nil
}

// ServeHTTP serves the OpenID discovery document and the JWKS.
func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.Issuer, "jwks_uri": p.Issuer + "/jwks",
			"authorization_endpoint": p.Issuer + "/auth", "token_endpoint": p.Issuer + "/token",
			"response_types_supported": []string{"id_token"}, "subject_types_supported": []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	case "/jwks":
		jwks, err := p.backend.GetJwks()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwks)
	default:
		http.NotFound(w, r)
	}
}

// Key is an opkssh login key: the user key and its certificate, which
// carries the PK Token.
type Key struct {
	Private crypto.Signer
	Cert    *ssh.Certificate
}

// Signer returns the SSH signer that logs in with the certificate.
func (k Key) Signer() (ssh.Signer, error) {
	s, err := ssh.NewSignerFromSigner(k.Private)
	if err != nil {
		return nil, fmt.Errorf("signer: %w", err)
	}
	cs, err := ssh.NewCertSigner(k.Cert, s)
	if err != nil {
		return nil, fmt.Errorf("cert signer: %w", err)
	}
	return cs, nil
}

// NewKey issues a PK Token whose ID token has the claims (they may
// override sub, iat and exp) and returns it in an opkssh certificate.
func (p *Provider) NewKey(ctx context.Context, claims map[string]any) (Key, error) {
	p.template.ExtraClaims = claims
	opk, err := client.New(p.op)
	if err != nil {
		return Key{}, fmt.Errorf("opk client: %w", err)
	}
	pkt, err := opk.Auth(ctx)
	if err != nil {
		return Key{}, fmt.Errorf("PK Token: %w", err)
	}
	smuggler, err := sshcert.New(pkt, nil, nil)
	if err != nil {
		return Key{}, fmt.Errorf("opkssh certificate: %w", err)
	}
	s, err := ssh.NewSignerFromSigner(opk.GetSigner())
	if err != nil {
		return Key{}, fmt.Errorf("signer: %w", err)
	}
	mas, err := ssh.NewSignerWithAlgorithms(s.(ssh.AlgorithmSigner), []string{s.PublicKey().Type()})
	if err != nil {
		return Key{}, fmt.Errorf("signer: %w", err)
	}
	cert, err := smuggler.SignCert(mas)
	if err != nil {
		return Key{}, fmt.Errorf("sign certificate: %w", err)
	}
	return Key{Private: opk.GetSigner(), Cert: cert}, nil
}
