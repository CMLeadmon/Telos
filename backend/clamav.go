package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// ClamAVScanner streams uploads to clamd with connect, read, and write
// deadlines, and reports signature freshness.
type ClamAVScanner struct {
	Addr           string
	ConnectTimeout time.Duration
	IOTimeout      time.Duration
	MaxSigAge      time.Duration
}

func defaultClamAVScanner() *ClamAVScanner {
	addr := os.Getenv("CLAMAV_ADDR")
	if addr == "" {
		addr = "telos-clamav:3310"
	}
	return &ClamAVScanner{
		Addr:           addr,
		ConnectTimeout: 5 * time.Second,
		IOTimeout:      30 * time.Second,
		MaxSigAge:      7 * 24 * time.Hour,
	}
}

var (
	errScanUnreachable = errors.New("scanner_unreachable")
	errScanTimeout     = errors.New("scanner_timeout")
	errScanProtocol    = errors.New("scanner_protocol")
)

// Scan streams r to clamd via INSTREAM and returns whether the content is
// clean. Read and write deadlines are renewed on each chunk so a stalled
// scanner fails closed rather than hanging.
func (c *ClamAVScanner) Scan(ctx context.Context, r io.Reader) (clean bool, status string, err error) {
	d := net.Dialer{Timeout: c.ConnectTimeout}
	conn, err := d.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return false, "unreachable", errScanUnreachable
	}
	defer conn.Close()

	renew := func() { _ = conn.SetDeadline(time.Now().Add(c.IOTimeout)) }
	renew()

	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return false, "error", errScanTimeout
	}

	buf := make([]byte, 32<<10)
	for {
		if ctx.Err() != nil {
			return false, "error", ctx.Err()
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			renew()
			var sz [4]byte
			binary.BigEndian.PutUint32(sz[:], uint32(n))
			if _, err := conn.Write(sz[:]); err != nil {
				return false, "error", errScanTimeout
			}
			if _, err := conn.Write(buf[:n]); err != nil {
				return false, "error", errScanTimeout
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return false, "error", rerr
		}
	}

	renew()
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return false, "error", errScanTimeout
	}

	resp, err := io.ReadAll(conn)
	if err != nil {
		return false, "error", errScanTimeout
	}
	s := string(resp)
	switch {
	case strings.Contains(s, "OK") && !strings.Contains(s, "FOUND"):
		return true, "clean", nil
	case strings.Contains(s, "FOUND"):
		return false, "infected", nil
	default:
		return false, "failed", errScanProtocol
	}
}

// Freshness returns the age of the loaded signature database (via the clamd
// VERSION command) and an error if it exceeds MaxSigAge.
func (c *ClamAVScanner) Freshness(ctx context.Context) (time.Duration, error) {
	d := net.Dialer{Timeout: c.ConnectTimeout}
	conn, err := d.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return 0, errScanUnreachable
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(c.IOTimeout))

	if _, err := conn.Write([]byte("zVERSION\x00")); err != nil {
		return 0, errScanTimeout
	}
	line, err := bufio.NewReader(conn).ReadString('\x00')
	if err != nil && err != io.EOF {
		return 0, errScanTimeout
	}
	age, perr := parseClamAVVersionAge(strings.TrimRight(line, "\x00\n"), time.Now())
	if perr != nil {
		return 0, perr
	}
	if age > c.MaxSigAge {
		return age, fmt.Errorf("clamav signatures are stale (%s old)", age.Round(time.Hour))
	}
	return age, nil
}

// parseClamAVVersionAge extracts the signature build date from a clamd VERSION
// string like "ClamAV 1.4.1/27400/Wed Jul 10 08:00:00 2024".
func parseClamAVVersionAge(version string, now time.Time) (time.Duration, error) {
	parts := strings.Split(version, "/")
	if len(parts) < 3 {
		return 0, errScanProtocol
	}
	built, err := time.Parse("Mon Jan _2 15:04:05 2006", strings.TrimSpace(parts[2]))
	if err != nil {
		return 0, errScanProtocol
	}
	return now.Sub(built), nil
}

var clamScanner = defaultClamAVScanner()
