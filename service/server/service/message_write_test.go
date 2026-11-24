package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
)

func TestWebSocketWritesAcrossProducts(t *testing.T) {
	ready := make(chan *MessageHandler, 1)
	done := make(chan struct{})
	upgrader := websocket.Upgrader{EnableCompression: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		h := NewMessageHandler(conn)
		ready <- h
		go h.Read()
		h.Write()
		close(done)
	}))
	defer server.Close()
	dialer := websocket.Dialer{EnableCompression: true}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	h := <-ready
	const rounds = 5
	for i := 0; i < rounds; i++ {
		for product, box := range h.boxes {
			box.Messages <- v2ray.Message{Product: product, ProduceTime: int64(i), Body: product}
		}
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	next := make(map[string]int64)
	for i := 0; i < rounds*len(h.boxes); i++ {
		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatal(err)
		}
		if _, ok := h.boxes[msg.Type]; !ok || msg.Body != msg.Type || msg.ProduceTime != next[msg.Type] {
			t.Fatalf("unexpected feed message %+v", msg)
		}
		next[msg.Type]++
	}
	conn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not exit after client disconnect")
	}
}
