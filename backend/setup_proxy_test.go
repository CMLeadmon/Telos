package main

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestParseSetupProxyMapsAcceptsOnlyTheFixedRelays(t *testing.T) {
	maps, err := parseSetupProxyMaps([]string{"--map", "0.0.0.0:8096=jellyfin:8096", "--map", "0.0.0.0:6060=grimmory:6060"})
	if err != nil || len(maps) != 2 {
		t.Fatalf("maps=%v err=%v", maps, err)
	}
	for _, bad := range [][]string{
		nil,
		{"--map"},
		{"--listen", "0.0.0.0:8096"},
		{"--map", "0.0.0.0:8096"},
		{"--map", "0.0.0.0:8096=evil.example:80"},
		{"--map", "0.0.0.0:8096=jellyfin:22"},
		{"--map", "0.0.0.0:9999=jellyfin:8096"},
		{"--map", "0.0.0.0:8096=grimmory:6060"},
		{"--map", "10.0.0.5:8096=jellyfin:8096"},
		{"--map", "[::]:8096=jellyfin:8096"},
		{"--map", "0.0.0.0:8096=jellyfin:8096", "--map", "127.0.0.1:8096=jellyfin:8096"},
	} {
		if _, err := parseSetupProxyMaps(bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestSetupProxyRelaysBytesBothWaysAndStopsOnCancel(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		for {
			conn, err := upstream.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				data, _ := io.ReadAll(conn)
				_, _ = conn.Write([]byte("echo:" + strings.ToUpper(string(data))))
			}()
		}
	}()
	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		serveSetupProxy(ctx, front, upstream.Addr().String(), make(chan struct{}, 4))
		close(done)
	}()
	client, err := net.Dial("tcp", front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = client.Write([]byte("hello"))
	_ = client.(*net.TCPConn).CloseWrite()
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	reply, err := io.ReadAll(client)
	if err != nil || string(reply) != "echo:HELLO" {
		t.Fatalf("reply=%q err=%v", reply, err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the relay did not stop after cancel")
	}
	if _, err := net.DialTimeout("tcp", front.Addr().String(), time.Second); err == nil {
		t.Fatal("the listener is still open after cancel")
	}
}

func TestSetupProxyBoundsConnections(t *testing.T) {
	front, _ := net.Listen("tcp", "127.0.0.1:0")
	hold, _ := net.Listen("tcp", "127.0.0.1:0")
	defer hold.Close()
	go func() {
		for {
			c, err := hold.Accept()
			if err != nil {
				return
			}
			_ = c // keep it open, never reply
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go serveSetupProxy(ctx, front, hold.Addr().String(), make(chan struct{}, 1))
	first, _ := net.Dial("tcp", front.Addr().String())
	defer first.Close()
	time.Sleep(100 * time.Millisecond)
	second, err := net.Dial("tcp", front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := second.Read(make([]byte, 1)); err == nil {
		t.Fatal("a connection beyond the bound was relayed")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("a connection beyond the bound was left open instead of closed")
	}
}
