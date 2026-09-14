package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEnvelope_HelloRoundTrip(t *testing.T) {
	hello := HelloPayload{
		Hostname:       "dev01",
		OS:             "linux",
		Arch:           "amd64",
		RuntimeVersion: "0.1.0",
		Capabilities: Capabilities{
			Terminal: false, Filesystem: false, Services: false,
			OSUsername: "brian", HomeDir: "/home/brian", Shells: []string{"/bin/bash", "/bin/sh"},
		},
	}
	payload, err := json.Marshal(hello)
	if err != nil {
		t.Fatalf("marshal hello: %v", err)
	}
	env := Envelope{Version: Version, Type: MessageHello, Payload: payload}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	var gotEnv Envelope
	if err := json.Unmarshal(data, &gotEnv); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if gotEnv.Version != Version || gotEnv.Type != MessageHello {
		t.Fatalf("envelope = %+v, want version %q type %q", gotEnv, Version, MessageHello)
	}

	var gotHello HelloPayload
	if err := json.Unmarshal(gotEnv.Payload, &gotHello); err != nil {
		t.Fatalf("unmarshal hello payload: %v", err)
	}
	if !reflect.DeepEqual(gotHello, hello) {
		t.Fatalf("hello = %+v, want %+v", gotHello, hello)
	}
}

func TestEnvelope_HeartbeatHasNoPayload(t *testing.T) {
	data, err := json.Marshal(Envelope{Version: Version, Type: MessageHeartbeat})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Type != MessageHeartbeat || env.Payload != nil {
		t.Fatalf("env = %+v, want heartbeat with no payload", env)
	}
}

func TestEncodeDecodeFrame_RoundTrip(t *testing.T) {
	sessionID := "123e4567-e89b-12d3-a456-426614174000"
	payload := []byte{0x00, 0x1b, '[', '2', 'J', 0xff, 0x80}

	frame := EncodeFrame(sessionID, payload)

	gotID, gotPayload, ok := DecodeFrame(frame)
	if !ok {
		t.Fatal("DecodeFrame reported ok=false")
	}
	if gotID != sessionID {
		t.Fatalf("sessionID = %q, want %q", gotID, sessionID)
	}
	if !reflect.DeepEqual(gotPayload, payload) {
		t.Fatalf("payload = %v, want %v", gotPayload, payload)
	}
}

func TestDecodeFrame_TooShort(t *testing.T) {
	if _, _, ok := DecodeFrame([]byte("short")); ok {
		t.Fatal("expected ok=false for a frame shorter than the session ID prefix")
	}
}
