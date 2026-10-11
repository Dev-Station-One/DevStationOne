package events

import "testing"

func TestBusBroadcastReachesEveryone(t *testing.T) {
	b := NewBus()
	chAlice, cancelAlice := b.Subscribe("alice")
	defer cancelAlice()
	chBob, cancelBob := b.Subscribe("bob")
	defer cancelBob()

	b.Publish(Event{Type: "runtime.status"})

	for name, ch := range map[string]<-chan Event{"alice": chAlice, "bob": chBob} {
		select {
		case ev := <-ch:
			if ev.Type != "runtime.status" {
				t.Fatalf("%s: got type %q, want runtime.status", name, ev.Type)
			}
		default:
			t.Fatalf("%s: did not receive broadcast event", name)
		}
	}
}

func TestBusUserScopedEventOnlyReachesThatUser(t *testing.T) {
	b := NewBus()
	chAlice, cancelAlice := b.Subscribe("alice")
	defer cancelAlice()
	chBob, cancelBob := b.Subscribe("bob")
	defer cancelBob()

	alice := "alice"
	b.Publish(Event{Type: "project.status", UserID: &alice})

	select {
	case <-chAlice:
	default:
		t.Fatal("alice: expected to receive her own event")
	}
	select {
	case <-chBob:
		t.Fatal("bob: should not receive alice's event")
	default:
	}
}

func TestBusCancelStopsDelivery(t *testing.T) {
	b := NewBus()
	ch, cancel := b.Subscribe("alice")
	cancel()

	b.Publish(Event{Type: "runtime.status"})

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected no delivery after cancel")
		}
	default:
	}
}

func TestBusFullBufferDropsRatherThanBlocks(t *testing.T) {
	b := NewBus()
	_, cancel := b.Subscribe("alice") // channel intentionally never drained
	defer cancel()

	for i := 0; i < subscriberBuffer+5; i++ {
		b.Publish(Event{Type: "runtime.status"}) // must not block/deadlock
	}
}

func TestBusTapSeesEveryEventRegardlessOfUserScope(t *testing.T) {
	b := NewBus()
	var seen []string
	b.Tap(func(e Event) { seen = append(seen, e.Type) })

	alice := "alice"
	b.Publish(Event{Type: "scoped", UserID: &alice}) // no subscribers at all
	b.Publish(Event{Type: "broadcast"})

	if len(seen) != 2 || seen[0] != "scoped" || seen[1] != "broadcast" {
		t.Fatalf("tap saw %v, want [scoped broadcast]", seen)
	}
}
