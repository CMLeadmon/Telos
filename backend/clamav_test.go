package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeClamd is a minimal clamd that speaks INSTREAM and VERSION.
func fakeClamd(t *testing.T, verdict string, versionLine string, stall bool) *ClamAVScanner {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				cmd, _ := br.ReadString('\x00') // command ends at NUL
				if strings.Contains(cmd, "VERSION") {
					c.Write([]byte(versionLine + "\x00"))
					return
				}
				if stall {
					time.Sleep(3 * time.Second)
					return
				}
				// Drain INSTREAM chunks until the zero-length terminator.
				for {
					var sz [4]byte
					if _, err := io.ReadFull(br, sz[:]); err != nil {
						return
					}
					length := binary.BigEndian.Uint32(sz[:])
					if length == 0 {
						break
					}
					io.CopyN(io.Discard, br, int64(length))
				}
				c.Write([]byte(verdict))
			}(conn)
		}
	}()
	return &ClamAVScanner{Addr: ln.Addr().String(), ConnectTimeout: time.Second, IOTimeout: 500 * time.Millisecond, MaxSigAge: 7 * 24 * time.Hour}
}

func TestClamAVScanClean(t *testing.T) {
	s := fakeClamd(t, "stream: OK\x00", "", false)
	clean, status, err := s.Scan(context.Background(), strings.NewReader("harmless content"))
	if err != nil || !clean || status != "clean" {
		t.Fatalf("clean scan = (%v,%q,%v)", clean, status, err)
	}
}

func TestClamAVScanInfected(t *testing.T) {
	s := fakeClamd(t, "stream: Eicar-Test-Signature FOUND\x00", "", false)
	clean, status, _ := s.Scan(context.Background(), strings.NewReader("bad"))
	if clean || status != "infected" {
		t.Fatalf("infected scan = (%v,%q)", clean, status)
	}
}

func TestClamAVScanStalledTimesOut(t *testing.T) {
	s := fakeClamd(t, "stream: OK\x00", "", true)
	start := time.Now()
	_, _, err := s.Scan(context.Background(), strings.NewReader(strings.Repeat("x", 1<<16)))
	if err == nil {
		t.Fatal("stalled scanner did not fail")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("scan did not honor the IO deadline")
	}
}

func TestClamAVUnreachable(t *testing.T) {
	s := &ClamAVScanner{Addr: "127.0.0.1:1", ConnectTimeout: 300 * time.Millisecond, IOTimeout: time.Second}
	if _, _, err := s.Scan(context.Background(), strings.NewReader("x")); err == nil {
		t.Fatal("unreachable scanner reported success")
	}
}

func TestClamAVFreshness(t *testing.T) {
	now := time.Now()

	t.Run("fresh", func(t *testing.T) {
		recent := now.Add(-24 * time.Hour).Format("Mon Jan _2 15:04:05 2006")
		s := fakeClamd(t, "", "ClamAV 1.4.1/27400/"+recent, false)
		if _, err := s.Freshness(context.Background()); err != nil {
			t.Fatalf("recent signatures reported stale: %v", err)
		}
	})

	t.Run("stale", func(t *testing.T) {
		old := now.Add(-30 * 24 * time.Hour).Format("Mon Jan _2 15:04:05 2006")
		s := fakeClamd(t, "", "ClamAV 1.4.1/27000/"+old, false)
		if _, err := s.Freshness(context.Background()); err == nil {
			t.Fatal("30-day-old signatures reported fresh")
		}
	})
}

func TestParseClamAVVersionAge(t *testing.T) {
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	age, err := parseClamAVVersionAge("ClamAV 1.4.1/27400/Mon Jul 20 08:00:00 2026", now)
	if err != nil {
		t.Fatal(err)
	}
	if age < 15*time.Hour || age > 20*time.Hour {
		t.Fatalf("age = %v, want ~16h", age)
	}
	if _, err := parseClamAVVersionAge("garbage", now); err == nil {
		t.Fatal("garbage version parsed")
	}
}
