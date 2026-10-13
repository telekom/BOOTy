package caprf

import (
	"bufio"
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/telekom/BOOTy/pkg/config"
)

// MergeCommandVars returns the config for one standby command: the base vars
// file with the payload lines appended, so a payload entry wins over the
// same key in the base file. The payload uses the vars file format. It
// returns the payload keys, sorted.
//
// The payload can not change the settings that the running ramdisk already
// uses: the transport (tokens, callback URLs, TLS), the agent endpoints, the
// network, MODE, DRY_RUN and CAPRF_LEGACY_COMPAT. Such a payload is an error.
func MergeCommandVars(base, payload []byte) (cfg *config.MachineConfig, keys []string, err error) {
	keys, err = payloadKeys(payload)
	if err != nil {
		return nil, nil, err
	}
	ref, err := ParseVars(bytes.NewReader(base))
	if err != nil {
		return nil, nil, fmt.Errorf("parse base vars: %w", err)
	}
	merged := slices.Concat(base, []byte("\n"), payload)
	cfg, err = ParseVars(bytes.NewReader(merged))
	if err != nil {
		return nil, nil, fmt.Errorf("payload: %w", err)
	}
	var fixed []string
	for _, s := range []struct {
		name      string
		ref, cand any
	}{
		{"transport", ref.Transport, cfg.Transport},
		{"agent", ref.Agent, cfg.Agent},
		{"network", ref.Network, cfg.Network},
		{"MODE", ref.Mode, cfg.Mode},
		{"DRY_RUN", ref.DryRun, cfg.DryRun},
		{"CAPRF_LEGACY_COMPAT", ref.LegacyCompat, cfg.LegacyCompat},
	} {
		if !reflect.DeepEqual(s.ref, s.cand) {
			fixed = append(fixed, s.name)
		}
	}
	if len(fixed) > 0 {
		return nil, nil, fmt.Errorf("payload changes %s settings, which can not change in a running ramdisk",
			strings.Join(fixed, ", "))
	}
	return cfg, keys, nil
}

// payloadKeys returns the sorted, unique keys of the payload lines.
func payloadKeys(payload []byte) ([]string, error) {
	var keys []string
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	for scanner.Scan() {
		if key, _, ok := parseVarLine(scanner.Text()); ok {
			keys = append(keys, strings.TrimSpace(key))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan payload: %w", err)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("payload has no KEY=VALUE line")
	}
	slices.Sort(keys)
	return slices.Compact(keys), nil
}
