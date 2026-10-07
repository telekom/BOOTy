// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestFanoutFilteringAndAttributesCharacterization(t *testing.T) {
	var debug, info bytes.Buffer
	debugHandler := slog.NewTextHandler(&debug, &slog.HandlerOptions{Level: slog.LevelDebug})
	infoHandler := slog.NewTextHandler(&info, &slog.HandlerOptions{Level: slog.LevelInfo})
	handler := NewMultiHandler(debugHandler, infoHandler).WithGroup("machine").
		WithAttrs([]slog.Attr{slog.String("serial", "fixture")})
	logger := slog.New(handler)
	logger.DebugContext(context.Background(), "debug-record")
	logger.InfoContext(context.Background(), "info-record")
	if strings.Contains(info.String(), "debug-record") {
		t.Fatal("debug record reached disabled sink")
	}
	for _, output := range []string{debug.String(), info.String()} {
		if !strings.Contains(output, "info-record") || !strings.Contains(output, "machine.serial=fixture") {
			t.Fatalf("fanout lost record or grouped attributes: %s", output)
		}
	}
	if !strings.Contains(debug.String(), "debug-record") {
		t.Fatal("debug sink did not receive enabled record")
	}
}
