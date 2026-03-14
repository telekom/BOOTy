package rescue

import (
	"fmt"
	"testing"
	"time"
)

func TestParseMode(t *testing.T) {
	tests := []struct {
		input   string
		want    Mode
		wantErr bool
	}{
		{"reboot", ModeReboot, false},
		{"shell", ModeShell, false},
		{"retry", ModeRetry, false},
		{"wait", ModeWait, false},
		{"bad", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseMode(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseMode(%q) err = %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"valid reboot", Config{Mode: ModeReboot}, false},
		{"valid retry", Config{Mode: ModeRetry, MaxRetries: 3}, false},
		{"retry no max", Config{Mode: ModeRetry}, true},
		{"bad mode", Config{Mode: "bad"}, true},
		{"negative delay", Config{Mode: ModeReboot, RetryDelay: -1}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestConfig_ApplyDefaults(t *testing.T) {
	cfg := &Config{}
	cfg.ApplyDefaults()

	if cfg.Mode != ModeReboot {
		t.Errorf("mode = %q", cfg.Mode)
	}
	if cfg.RetryDelay != 30*time.Second {
		t.Errorf("retryDelay = %v", cfg.RetryDelay)
	}
	if cfg.ShellTimeout != 30*time.Minute {
		t.Errorf("shellTimeout = %v", cfg.ShellTimeout)
	}
}

func TestConfig_ApplyDefaults_Retry(t *testing.T) {
	cfg := &Config{Mode: ModeRetry}
	cfg.ApplyDefaults()

	if cfg.MaxRetries != 3 {
		t.Errorf("maxRetries = %d", cfg.MaxRetries)
	}
}

func TestRetryState_CanRetry(t *testing.T) {
	s := &RetryState{Attempts: 0, MaxRetries: 3}
	if !s.CanRetry() {
		t.Error("should be able to retry")
	}

	s.Attempts = 3
	if s.CanRetry() {
		t.Error("should not be able to retry")
	}
}

func TestRetryState_RecordAttempt(t *testing.T) {
	s := &RetryState{MaxRetries: 3}
	s.RecordAttempt(fmt.Errorf("disk error"))

	if s.Attempts != 1 {
		t.Errorf("attempts = %d", s.Attempts)
	}
	if s.LastError != "disk error" {
		t.Errorf("lastError = %q", s.LastError)
	}
	if s.LastRetry.IsZero() {
		t.Error("lastRetry is zero")
	}
}

func TestRetryState_RecordAttempt_NilErr(t *testing.T) {
	s := &RetryState{MaxRetries: 3}
	s.RecordAttempt(nil)

	if s.LastError != "" {
		t.Errorf("lastError = %q", s.LastError)
	}
}

func TestRetryState_Remaining(t *testing.T) {
	s := &RetryState{Attempts: 1, MaxRetries: 3}
	if s.Remaining() != 2 {
		t.Errorf("remaining = %d", s.Remaining())
	}

	s.Attempts = 5
	if s.Remaining() != 0 {
		t.Errorf("remaining should be 0, got %d", s.Remaining())
	}
}

func TestDecide_Retry(t *testing.T) {
	cfg := &Config{Mode: ModeRetry, MaxRetries: 3}
	state := &RetryState{MaxRetries: 3, Attempts: 0}
	action := Decide(cfg, state)

	if action.Type != ModeRetry {
		t.Errorf("type = %q", action.Type)
	}
}

func TestDecide_RetryExhausted(t *testing.T) {
	cfg := &Config{Mode: ModeRetry, MaxRetries: 3}
	state := &RetryState{MaxRetries: 3, Attempts: 3}
	action := Decide(cfg, state)

	if action.Type != ModeReboot {
		t.Errorf("type = %q, want reboot", action.Type)
	}
}

func TestDecide_Shell(t *testing.T) {
	cfg := &Config{Mode: ModeShell, ShellTimeout: 10 * time.Minute}
	action := Decide(cfg, &RetryState{})

	if action.Type != ModeShell {
		t.Errorf("type = %q", action.Type)
	}
}

func TestDecide_Wait(t *testing.T) {
	cfg := &Config{Mode: ModeWait}
	action := Decide(cfg, &RetryState{})

	if action.Type != ModeWait {
		t.Errorf("type = %q", action.Type)
	}
}

func TestDecide_Default(t *testing.T) {
	cfg := &Config{Mode: ModeReboot}
	action := Decide(cfg, &RetryState{})

	if action.Type != ModeReboot {
		t.Errorf("type = %q", action.Type)
	}
}

func TestModeConstants(t *testing.T) {
	if string(ModeReboot) != "reboot" {
		t.Error("ModeReboot")
	}
	if string(ModeShell) != "shell" {
		t.Error("ModeShell")
	}
	if string(ModeRetry) != "retry" {
		t.Error("ModeRetry")
	}
	if string(ModeWait) != "wait" {
		t.Error("ModeWait")
	}
}
