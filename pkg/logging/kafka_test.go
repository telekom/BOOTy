package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

func TestKafkaConfig_Validate(t *testing.T) {
	tests := []struct {
		name string
		cfg  KafkaConfig
		err  bool
	}{
		{"valid", KafkaConfig{Brokers: []string{"localhost:9092"}, Topic: "t"}, false},
		{"no brokers", KafkaConfig{Topic: "t"}, true},
		{"no topic", KafkaConfig{Brokers: []string{"localhost:9092"}}, true},
		{"empty broker", KafkaConfig{Brokers: []string{""}, Topic: "t"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.err {
				t.Errorf("Validate() err = %v, wantErr %v", err, tc.err)
			}
		})
	}
}

type mockWriter struct {
	messages [][]byte
}

func (m *mockWriter) WriteMessage(_ string, _ string, data []byte) error {
	m.messages = append(m.messages, append([]byte(nil), data...))
	return nil
}

func (m *mockWriter) Close() error { return nil }

func TestKafkaHandler_Handle(t *testing.T) {
	mw := &mockWriter{}
	id := MachineIdentity{
		Serial:         "SN123",
		BMCMAC:         "aa:bb:cc:dd:ee:ff",
		ProvisioningID: "prov-001",
	}
	h := NewKafkaHandler(mw, "test.topic", id)

	r := slog.NewRecord(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), slog.LevelInfo, "test message", 0)
	r.AddAttrs(slog.String("step", "disk-image"))

	err := h.Handle(context.Background(), r)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(mw.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(mw.messages))
	}

	var msg LogMessage
	if err := json.Unmarshal(mw.messages[0], &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if msg.MachineSerial != "SN123" {
		t.Errorf("serial = %q", msg.MachineSerial)
	}
	if msg.Message != "test message" {
		t.Errorf("message = %q", msg.Message)
	}
	if msg.Step != "disk-image" {
		t.Errorf("step = %q", msg.Step)
	}
	if msg.Level != "INFO" {
		t.Errorf("level = %q", msg.Level)
	}
}

func TestKafkaHandler_Enabled(t *testing.T) {
	mw := &mockWriter{}
	h := NewKafkaHandler(mw, "t", MachineIdentity{})

	if !h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("should be enabled for INFO")
	}
	if h.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("should not be enabled for DEBUG")
	}
}

func TestKafkaHandler_WithAttrs(t *testing.T) {
	mw := &mockWriter{}
	h := NewKafkaHandler(mw, "t", MachineIdentity{})
	h2 := h.WithAttrs([]slog.Attr{slog.String("step", "network")})

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	if err := h2.Handle(context.Background(), r); err != nil {
		t.Fatal(err)
	}

	var msg LogMessage
	json.Unmarshal(mw.messages[0], &msg)
	if msg.Step != "network" {
		t.Errorf("step = %q, want network", msg.Step)
	}
}

func TestKafkaHandler_WithGroup(t *testing.T) {
	mw := &mockWriter{}
	h := NewKafkaHandler(mw, "t", MachineIdentity{})
	h2 := h.WithGroup("sub")
	if h2 == nil {
		t.Error("WithGroup returned nil")
	}
}

func TestKafkaHandler_Close(t *testing.T) {
	mw := &mockWriter{}
	h := NewKafkaHandler(mw, "t", MachineIdentity{})
	if err := h.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestWriterAdapter(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriterAdapter(&buf)

	err := w.WriteMessage("topic", "key", []byte(`{"msg":"hello"}`))
	if err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	if buf.String() != "{\"msg\":\"hello\"}\n" {
		t.Errorf("output = %q", buf.String())
	}

	if err := w.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestMultiHandler(t *testing.T) {
	mw1 := &mockWriter{}
	mw2 := &mockWriter{}
	h1 := NewKafkaHandler(mw1, "t1", MachineIdentity{Serial: "A"})
	h2 := NewKafkaHandler(mw2, "t2", MachineIdentity{Serial: "B"})

	multi := NewMultiHandler(h1, h2)

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "multi", 0)
	if err := multi.Handle(context.Background(), r); err != nil {
		t.Fatal(err)
	}

	if len(mw1.messages) != 1 {
		t.Errorf("mw1 = %d", len(mw1.messages))
	}
	if len(mw2.messages) != 1 {
		t.Errorf("mw2 = %d", len(mw2.messages))
	}
}

func TestMultiHandler_Enabled(t *testing.T) {
	mw := &mockWriter{}
	h := NewKafkaHandler(mw, "t", MachineIdentity{})
	multi := NewMultiHandler(h)

	if !multi.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("multi should be enabled for INFO")
	}
}

func TestMultiHandler_WithAttrs(t *testing.T) {
	mw := &mockWriter{}
	h := NewKafkaHandler(mw, "t", MachineIdentity{})
	multi := NewMultiHandler(h)
	m2 := multi.WithAttrs([]slog.Attr{slog.String("k", "v")})
	if m2 == nil {
		t.Error("WithAttrs returned nil")
	}
}

func TestMultiHandler_WithGroup(t *testing.T) {
	mw := &mockWriter{}
	h := NewKafkaHandler(mw, "t", MachineIdentity{})
	multi := NewMultiHandler(h)
	m2 := multi.WithGroup("g")
	if m2 == nil {
		t.Error("WithGroup returned nil")
	}
}

func TestLogMessage_JSON(t *testing.T) {
	msg := LogMessage{
		Timestamp:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		Level:          "INFO",
		Message:        "test",
		MachineSerial:  "SN",
		BMCMAC:         "mac",
		ProvisioningID: "prov",
		Step:           "step",
		Attrs:          map[string]string{"k": "v"},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var decoded LogMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.MachineSerial != "SN" {
		t.Errorf("serial = %q", decoded.MachineSerial)
	}
}

func TestMultiHandler_EmptyHandlers(t *testing.T) {
	multi := NewMultiHandler()
	if multi.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("empty multi should not be enabled")
	}
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "test", 0)
	if err := multi.Handle(context.Background(), r); err != nil {
		t.Errorf("Handle on empty multi: %v", err)
	}
}
