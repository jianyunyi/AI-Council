package group

import (
	"testing"
	"time"
)

func TestHubPublishKickAndSlowConsumer(t *testing.T) {
	h := NewHub(2)
	a := h.Subscribe("g1", "u1")
	b := h.Subscribe("g1", "u2")
	other := h.Subscribe("g2", "u1")

	h.Publish("g1", Event{Seq: 1, Type: "x", At: time.Now()})
	if ev := <-a.C; ev.Seq != 1 {
		t.Fatalf("a got %+v", ev)
	}
	<-b.C
	select {
	case <-other.C:
		t.Fatal("other group must not receive")
	default:
	}

	h.Kick("g1", "u1")
	if _, open := <-a.C; open {
		t.Fatal("kicked subscription must be closed")
	}
	if h.SubscriberCount("g1") != 1 {
		t.Fatalf("count=%d", h.SubscriberCount("g1"))
	}

	// b has buffer 2: the third publish without draining disconnects it.
	h.Publish("g1", Event{Seq: 2})
	h.Publish("g1", Event{Seq: 3})
	h.Publish("g1", Event{Seq: 4})
	<-b.C
	<-b.C
	if _, open := <-b.C; open {
		t.Fatal("slow consumer must be closed")
	}
	b.Close() // double close is safe
	other.Close()
}
