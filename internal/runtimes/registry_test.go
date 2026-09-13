package runtimes

import (
	"context"
	"encoding/json"
	"testing"

	"devstation/internal/protocol"
)

type fakeConn struct {
	revoked bool
}

func (c *fakeConn) Revoke() error { c.revoked = true; return nil }

func (c *fakeConn) SendEnvelope(msgType protocol.MessageType, payload any) error { return nil }

func (c *fakeConn) SendFrame(sessionID string, data []byte) error { return nil }

func (c *fakeConn) SendCommand(ctx context.Context, msgType protocol.MessageType, payload any) (json.RawMessage, error) {
	return nil, nil
}

func TestRegistry_RegisterUnregisterIsOnline(t *testing.T) {
	r := NewRegistry()
	if r.IsOnline("rt1") {
		t.Fatal("new registry should report offline")
	}

	c := &fakeConn{}
	r.Register("rt1", c)
	if !r.IsOnline("rt1") {
		t.Fatal("expected rt1 online after Register")
	}

	r.Unregister("rt1", c)
	if r.IsOnline("rt1") {
		t.Fatal("expected rt1 offline after Unregister")
	}
}

func TestRegistry_UnregisterIgnoresStaleConn(t *testing.T) {
	r := NewRegistry()
	old := &fakeConn{}
	newer := &fakeConn{}

	r.Register("rt1", old)
	r.Register("rt1", newer) // simulate a reconnect replacing the old conn

	r.Unregister("rt1", old) // stale disconnect handler for the old conn
	if !r.IsOnline("rt1") {
		t.Fatal("stale Unregister must not evict the newer connection")
	}
}

func TestRegistry_Revoke(t *testing.T) {
	r := NewRegistry()

	if r.Revoke("missing") {
		t.Fatal("Revoke on unknown runtime should report false")
	}

	c := &fakeConn{}
	r.Register("rt1", c)
	if !r.Revoke("rt1") {
		t.Fatal("Revoke on registered runtime should report true")
	}
	if !c.revoked {
		t.Fatal("expected underlying connection to be revoked")
	}
}

func TestRegistry_Get(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("rt1"); ok {
		t.Fatal("Get on unknown runtime should report false")
	}

	c := &fakeConn{}
	r.Register("rt1", c)
	got, ok := r.Get("rt1")
	if !ok || got != c {
		t.Fatalf("Get = %v, %v, want registered conn, true", got, ok)
	}
}
