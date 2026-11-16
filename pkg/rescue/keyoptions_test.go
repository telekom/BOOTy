package rescue

import (
	"reflect"
	"testing"
)

func TestParseAuthorizedKeyOptions(t *testing.T) {
	for _, tc := range []struct {
		name, options string
		want          keyOptions
		bad           bool
	}{
		{name: "none"},
		{name: "no-port-forwarding", options: "no-port-forwarding", want: keyOptions{noPortForwarding: true}},
		{name: "no-pty, any case", options: "No-PTY", want: keyOptions{noPTY: true}},
		{name: "restrict", options: "restrict", want: keyOptions{noPortForwarding: true, noPTY: true}},
		{name: "agent and X11 are never offered", options: "no-agent-forwarding,no-X11-forwarding"},
		{name: "no-touch-required", options: "no-touch-required", want: keyOptions{noTouchRequired: true}},
		{name: "command as written", options: `command="echo \"hi\"; id"`,
			want: keyOptions{command: `echo \"hi\"; id`, hasCommand: true}},
		{name: "permitopen", options: `permitopen="10.0.0.1:22",permitopen="db:*"`,
			want: keyOptions{permitOpen: []permitOpen{{"10.0.0.1", 22}, {"db", -1}}}},
		{name: "two commands", options: `command="a",command="b"`, bad: true},
		{name: "bad permitopen", options: `permitopen="nohost"`, bad: true},
		{name: "from= is unknown to dropbear", options: `from="10.0.0.0/8"`, bad: true},
		{name: "environment= is unknown to dropbear", options: `environment="A=b"`, bad: true},
		{name: "verify-required cannot be checked", options: "verify-required", bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := testKeyA
			if tc.options != "" {
				entry = tc.options + " " + testKeyA
			}
			got, err := parseAuthorizedKey(entry)
			if tc.bad != (err != nil) {
				t.Fatalf("err = %v, want bad=%v", err, tc.bad)
			}
			if err == nil && !reflect.DeepEqual(got.opts, tc.want) {
				t.Errorf("options = %+v, want %+v", got.opts, tc.want)
			}
		})
	}
}

func TestPermitOpen(t *testing.T) {
	k := keyOptions{permitOpen: []permitOpen{{"10.0.0.1", 22}, {"db", -1}}}
	for dest, want := range map[[2]any]bool{
		{"10.0.0.1", 22}: true, {"10.0.0.1", 23}: false, {"db", 5432}: true, {"other", 22}: false,
	} {
		if got := k.allowsOpen(dest[0].(string), dest[1].(int)); got != want {
			t.Errorf("allowsOpen(%v) = %v, want %v", dest, got, want)
		}
	}
	if !(&keyOptions{}).allowsOpen("any", 1) || (&keyOptions{noPortForwarding: true}).allowsOpen("any", 1) {
		t.Error("allowsOpen without permitopen")
	}
}
