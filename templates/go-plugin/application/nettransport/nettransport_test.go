package nettransport

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

func TestRoundTripOverPipe(t *testing.T) {
	a, b := net.Pipe()
	ta := New(a)
	tb := New(b)
	defer ta.Close()
	defer tb.Close()

	want := transport.Message{CallID: 7, Kind: transport.KindUnary, Method: "m", Body: []byte(`{"x":1}`)}
	go func() {
		_ = ta.Send(context.Background(), want)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err := tb.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if got.CallID != want.CallID || got.Kind != want.Kind || got.Method != want.Method || string(got.Body) != string(want.Body) {
		t.Fatalf("roundtrip mismatch: got %+v, want %+v", got, want)
	}
}

func TestRecvHonorsContextCancel(t *testing.T) {
	a, b := net.Pipe()
	ta := New(a)
	tb := New(b)
	defer ta.Close()
	defer tb.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err := tb.Recv(ctx)
	if err == nil {
		t.Fatal("Recv should have failed on canceled context")
	}
	if time.Since(start) > time.Second {
		t.Fatal("Recv did not honor canceled context promptly")
	}
}
