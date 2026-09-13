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
		Capabilities:   Capabilities{Terminal: false, Filesystem: false, Services: false},
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
