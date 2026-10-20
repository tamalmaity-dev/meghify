package realtime

import "testing"

func TestSlowClientChannelIsClosedAndReusedSafely(t *testing.T) {
	client := newClient(1)
	if !client.send(Event{Channel: "deployment:one"}) {
		t.Fatal("first event should fit the buffer")
	}
	if client.send(Event{Channel: "deployment:two"}) {
		t.Fatal("second event should overflow the buffer")
	}
	// The stream goroutine reads from this channel; closing it is what makes
	// the handler return instead of keepalive forever.
	if _, ok := <-client.ch; !ok {
		t.Fatal("the queued event should still be readable before the close")
	}
	if _, ok := <-client.ch; ok {
		t.Fatal("channel should be closed after the overflow")
	}
	// Broadcast calls remove after send fails, which calls close again.
	client.close()
}

func TestValidChannelAcceptsOnlyResourceStreams(t *testing.T) {
	for _, channel := range []string{
		"deployment:abc",
		"application:abc",
		"node:abc",
	} {
		if !validChannel(channel) {
			t.Fatalf("validChannel(%q) = false", channel)
		}
	}
	for _, channel := range []string{
		"",
		"deployment:",
		"global",
		"users:abc",
		"deployment:abc\n",
	} {
		if validChannel(channel) {
			t.Fatalf("validChannel(%q) = true", channel)
		}
	}
}
