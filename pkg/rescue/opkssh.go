//go:build linux && !no_opkssh

package rescue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/openpubkey/openpubkey/discover"
	"github.com/openpubkey/openpubkey/providers"
	"github.com/openpubkey/openpubkey/verifier"
	"github.com/openpubkey/opkssh/sshcert"
	"golang.org/x/crypto/ssh"

	"github.com/telekom/BOOTy/pkg/network"
	"github.com/telekom/BOOTy/pkg/timesync"
)

// rescueUser is the only login of the rescue shell.
const rescueUser = "root"

// opkProvider is one row of RESCUE_SSH_OPK_PROVIDERS, the opkssh
// /etc/opk/providers format: "issuer client_id expiration".
type opkProvider struct {
	issuer, clientID string
	expiration       verifier.ExpirationPolicy
}

// opkRule is one row of RESCUE_SSH_OPK_POLICY, the opkssh /etc/opk/auth_id
// format: "principal identity issuer".
type opkRule struct {
	principal, identity, issuer string
}

// opkExpirations are the opkssh expiration policies BOOTy accepts. "never"
// and "oidc_refreshed" (whose refreshed ID token opkssh checks only for its
// expiry) are refused.
var opkExpirations = map[string]verifier.ExpirationPolicy{
	"12h":   verifier.ExpirationPolicies.MAX_AGE_12HOURS,
	"24h":   verifier.ExpirationPolicies.MAX_AGE_24HOURS,
	"48h":   verifier.ExpirationPolicies.MAX_AGE_48HOURS,
	"1week": verifier.ExpirationPolicies.MAX_AGE_1WEEK,
	"oidc":  verifier.ExpirationPolicies.OIDC,
}

// opkRows splits an opkssh table value into rows of fields. Rows are
// separated by ";" or newlines; empty rows and "#" comments are skipped.
func opkRows(value string) [][]string {
	var rows [][]string
	for _, row := range strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == '\n' || r == '\r' }) {
		row, _, _ = strings.Cut(row, "#")
		if f := strings.Fields(row); len(f) > 0 {
			rows = append(rows, f)
		}
	}
	return rows
}

// parseOPKProviders parses RESCUE_SSH_OPK_PROVIDERS. A malformed row is
// logged by its position and skipped, like a malformed RESCUE_SSH_PUBKEY
// entry.
func parseOPKProviders(value string) []opkProvider {
	var out []opkProvider
	for i, f := range opkRows(value) {
		reason := ""
		var exp verifier.ExpirationPolicy
		known := false
		if len(f) == 3 {
			exp, known = opkExpirations[f[2]]
		}
		switch {
		case len(f) != 3:
			reason = "want 3 fields: issuer client_id expiration"
		case !strings.HasPrefix(f[0], "https://"):
			reason = "the issuer is not an https:// URL"
		case !known:
			reason = "unsupported expiration policy " + f[2] + " (12h, 24h, 48h, 1week, oidc)"
		}
		if reason != "" {
			slog.Warn("ignoring malformed RESCUE_SSH_OPK_PROVIDERS row", "row", i+1, "reason", reason)
			continue
		}
		out = append(out, opkProvider{issuer: f[0], clientID: f[1], expiration: exp})
	}
	return out
}

// parseOPKPolicy parses RESCUE_SSH_OPK_POLICY. Identities are an email
// address (matched without letter case) or a subject, or
// oidc:<claim>:<value> for a value of a claim such as groups.
func parseOPKPolicy(value string) []opkRule {
	var out []opkRule
	for i, f := range opkRows(value) {
		reason := ""
		switch {
		case len(f) != 3:
			reason = "want 3 fields: principal identity issuer"
		case strings.HasPrefix(f[1], "oidc-match-end:"):
			reason = "oidc-match-end identities are not supported"
		case strings.HasPrefix(f[1], "oidc:") && len(strings.SplitN(f[1], ":", 3)) != 3:
			reason = "want oidc:<claim>:<value>"
		}
		if reason != "" {
			slog.Warn("ignoring malformed RESCUE_SSH_OPK_POLICY row", "row", i+1, "reason", reason)
			continue
		}
		out = append(out, opkRule{principal: f[0], identity: f[1], issuer: f[2]})
	}
	return out
}

// opkClaims are the ID token claims that the policy and the log use.
type opkClaims struct {
	Issuer  string         `json:"iss"`
	Subject string         `json:"sub"`
	Email   string         `json:"email"`
	All     map[string]any `json:"-"`
}

// opkVerifier checks opkssh SSH certificates: the PK Token against the
// providers, then the identity against the policy.
type opkVerifier struct {
	pkt   *verifier.Verifier
	rules []opkRule
}

// newOPKVerifier returns the verifier of the providers. JWKS documents are
// fetched with client, without a cache: logins are rare and a rotated key
// works at once.
func newOPKVerifier(list []opkProvider, rules []opkRule, client *http.Client) (*opkVerifier, error) {
	finder := &discover.PublicKeyFinder{JwksFunc: func(ctx context.Context, issuer string) ([]byte, error) {
		return discover.GetJwksByIssuer(ctx, issuer, client)
	}}
	pvs := make([]verifier.ProviderVerifier, 0, len(list))
	for _, p := range list {
		pvs = append(pvs, verifier.ProviderVerifierExpires{
			ProviderVerifier: providers.NewProviderVerifier(p.issuer, providers.ProviderVerifierOpts{
				CommitType:        providers.CommitTypesEnum.NONCE_CLAIM,
				ClientID:          p.clientID,
				DiscoverPublicKey: finder,
			}),
			Expiration: p.expiration,
		})
	}
	if len(pvs) == 0 {
		return nil, errors.New("no valid RESCUE_SSH_OPK_PROVIDERS row")
	}
	v, err := verifier.NewFromMany(pvs)
	if err != nil {
		return nil, fmt.Errorf("opkssh verifier: %w", err)
	}
	return &opkVerifier{pkt: v, rules: rules}, nil
}

// jwksHTTPClient returns the client for the OIDC discovery and JWKS
// requests: the BOOTy HTTPS client (system roots and CA_BUNDLE_FILE), TLS
// verification also with SKIP_TLS_VERIFY, and no redirects.
func jwksHTTPClient() *http.Client {
	c := network.NewHTTPClient(30 * time.Second)
	if t, ok := c.Transport.(*http.Transport); ok {
		t.TLSClientConfig.InsecureSkipVerify = false
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("the OIDC provider answered with a redirect, which BOOTy does not follow")
	}
	return c
}

// verify checks an opkssh certificate for a login as user. It returns the
// claims of the PK Token; on an error they are unverified and only for the
// log.
func (v *opkVerifier) verify(ctx context.Context, user string, cert *ssh.Certificate) (opkClaims, error) {
	smuggler := &sshcert.SshCertSmuggler{SshCert: cert}
	var claims opkClaims
	pkt, err := smuggler.GetPKToken()
	if err != nil {
		return claims, fmt.Errorf("PK Token: %w", err)
	}
	_ = json.Unmarshal(pkt.Payload, &claims)
	_ = json.Unmarshal(pkt.Payload, &claims.All)
	if user != rescueUser {
		return claims, fmt.Errorf("only %s can log in", rescueUser)
	}
	if cert.CertType != ssh.UserCert {
		return claims, errors.New("not a user certificate")
	}
	// The certificate is signed by its own key (opkssh has no CA): check
	// its validity period and signature like sshd's cert-authority line.
	if err := (&ssh.CertChecker{}).CheckCert(user, cert); err != nil {
		return claims, fmt.Errorf("certificate: %w", err)
	}
	if _, err := smuggler.VerifySshPktCert(ctx, *v.pkt); err != nil {
		return claims, fmt.Errorf("PK Token: %w", err)
	}
	if !slices.ContainsFunc(v.rules, func(r opkRule) bool { return r.matches(user, claims) }) {
		return claims, fmt.Errorf("no RESCUE_SSH_OPK_POLICY row allows this identity as %s", user)
	}
	return claims, nil
}

// matches reports whether the rule allows the verified claims to log in as
// principal, like the opkssh policy: the issuer is equal, an email without
// letter case, a subject exactly, an oidc:<claim>:<value> value exactly.
func (r opkRule) matches(principal string, c opkClaims) bool {
	if r.principal != principal || r.issuer != c.Issuer {
		return false
	}
	if rest, ok := strings.CutPrefix(r.identity, "oidc:"); ok {
		claim, want, _ := strings.Cut(rest, ":")
		switch v := c.All[claim].(type) {
		case string:
			return v == want
		case []any:
			return slices.Contains(v, any(want))
		}
		return false
	}
	return (c.Email != "" && strings.EqualFold(c.Email, r.identity)) || c.Subject == r.identity
}

// jwtPattern matches a compact JWT, which a library error could quote.
var jwtPattern = regexp.MustCompile(`eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_.-]*`)

// refusalReason is the log reason of a refused login, without any token and
// with a hint when the clock was not synced.
func refusalReason(err error) string {
	reason := jwtPattern.ReplaceAllString(err.Error(), "<token>")
	if len(reason) > 300 {
		reason = reason[:300] + "..."
	}
	if !timesync.Synced() {
		reason += " (the clock was not synced, the token times are checked against the RTC; see TIME_SYNC)"
	}
	return reason
}
