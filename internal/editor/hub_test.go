package editor

import (
	"testing"
)

func TestHubSubscribeWatchPublish(t *testing.T) {
	h := NewHub()

	s1, ch1, cancel1 := h.Subscribe("p1")
	defer cancel1()
	s2, ch2, cancel2 := h.Subscribe("p1")
	defer cancel2()
	_, chOther, cancelOther := h.Subscribe("p2")
	defer cancelOther()

	// Two subscribers with overlapping sets produce one union.
	if union := h.SetPaths("p1", s1, []string{"a.go", "b.go"}); len(union) != 2 {
		t.Errorf("union after s1 = %v, want 2 paths", union)
	}
	if union := h.SetPaths("p1", s2, []string{"b.go", "c.go"}); len(union) != 3 {
		t.Errorf("union after s2 = %v, want 3 paths", union)
	}

	// Publish reaches both project subscribers, not other projects'.
	h.Publish("p1", Message{Type: "content", Path: "a.go", Data: "x"})
	for i, ch := range []<-chan Message{ch1, ch2} {
		select {
		case m := <-ch:
			if m.Path != "a.go" || m.Data != "x" {
				t.Errorf("subscriber %d got %+v", i, m)
			}
		default:
			t.Errorf("subscriber %d got nothing", i)
		}
	}
	select {
	case m := <-chOther:
		t.Errorf("p2 subscriber got %+v", m)
	default:
	}

	// Dropping one subscriber shrinks the union; dropping the last empties it.
	if union := h.Drop("p1", s2); len(union) != 2 {
		t.Errorf("union after dropping s2 = %v, want 2 paths", union)
	}
	if union := h.Drop("p1", s1); union != nil {
		t.Errorf("union after dropping the last subscriber = %v, want nil", union)
	}
}

func TestHubSlowSubscriberDrops(t *testing.T) {
	h := NewHub()
	s, ch, cancel := h.Subscribe("p")
	defer cancel()
	h.SetPaths("p", s, []string{"a.txt"})

	for i := 0; i < subscriberBuffer+10; i++ {
		h.Publish("p", Message{Type: "content", Path: "a.txt"})
	}
	// The buffer holds the first subscriberBuffer messages; the overflow is
	// dropped, and the subscriber must still be live for the next publish.
	count := 0
drain:
	for {
		select {
		case <-ch:
			count++
		default:
			break drain
		}
	}
	if count != subscriberBuffer {
		t.Errorf("drained %d messages, want %d", count, subscriberBuffer)
	}
}
