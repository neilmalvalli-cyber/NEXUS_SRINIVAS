package proc

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The test binary doubles as a fake kvdb (a mock engine, used only to test
// the process manager itself, never as evidence about NEXUS).
func TestMain(m *testing.M) {
	switch os.Getenv("TORTURE_FAKE") {
	case "":
		os.Exit(m.Run())
	case "usage":
		fmt.Fprintln(os.Stderr, "usage: kvdb ...")
		os.Exit(2)
	case "ack":
		fmt.Fprintln(os.Stderr, "serve: ready, 0 keys")
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) >= 2 {
				fmt.Printf("ACK %s\n", f[1])
			}
		}
		os.Exit(0)
	case "partial":
		fmt.Fprintln(os.Stderr, "serve: ready, 0 keys")
		sc := bufio.NewScanner(os.Stdin)
		sc.Scan()
		f := strings.Fields(sc.Text())
		fmt.Printf("ACK %s", f[1]) // no newline: an incomplete reply
		os.Stdout.Sync()
		os.Exit(0)
	case "hang":
		fmt.Fprintln(os.Stderr, "serve: ready, 0 keys")
		time.Sleep(time.Hour)
	case "garbage":
		fmt.Fprintln(os.Stderr, "serve: ready, 0 keys")
		fmt.Println("HELLO world")
		fmt.Println("ACK nobody-asked")
		time.Sleep(time.Hour)
	}
	os.Exit(3)
}

func fake(mode string) Binary {
	return Binary{Path: os.Args[0], Env: []string{"TORTURE_FAKE=" + mode}}
}

func clock() func() int64 {
	t0 := time.Now()
	return func() int64 { return time.Since(t0).Nanoseconds() }
}

func TestLocateFailsClearly(t *testing.T) {
	_, err := Locate(filepath.Join(t.TempDir(), "no-such-kvdb.exe"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing binary: %v", err)
	}
	_, err = Locate(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("directory as binary: %v", err)
	}
	// A runnable program that is not kvdb (the test binary without the
	// fake mode runs the tests and exits 0) is rejected.
	r := fake("usage").Run(10*time.Second, nil)
	if r.Exit != 2 || !strings.Contains(r.Stderr, "usage") {
		t.Fatalf("fake usage: %+v", r)
	}
}

func TestCompleteAckDelivered(t *testing.T) {
	s, err := fake("ack").StartServe("db", nil, clock())
	if err != nil {
		t.Fatal(err)
	}
	if !s.WaitReady(10 * time.Second) {
		t.Fatal("no ready line")
	}
	ch, err := s.Send("op1", []byte("w1 op1 put k v\n"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case rp, ok := <-ch:
		if !ok || rp.Kind != "ACK" || rp.ID != "op1" || rp.At <= 0 {
			t.Fatalf("reply %+v ok=%v", rp, ok)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout")
	}
	s.CloseInput()
	if code, ok := s.Wait(10 * time.Second); !ok || code != 0 {
		t.Fatalf("exit %d ok=%v", code, ok)
	}
}

func TestPartialAckIsNotAnAck(t *testing.T) {
	s, err := fake("partial").StartServe("db", nil, clock())
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := s.Send("op1", []byte("w1 op1 put k v\n"))
	select {
	case rp, ok := <-ch:
		if ok {
			t.Fatalf("an incomplete line was delivered as a reply: %+v", rp)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("channel not closed at EOF")
	}
	s.Wait(10 * time.Second)
}

func TestHangIsDetectedByTimeoutAndKilled(t *testing.T) {
	s, err := fake("hang").StartServe("db", nil, clock())
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := s.Send("op1", []byte("w1 op1 put k v\n"))
	select {
	case <-ch:
		t.Fatal("hung server produced a reply")
	case <-time.After(300 * time.Millisecond):
	}
	if !s.Kill() {
		t.Fatal("kill did not land on a running process")
	}
	if _, ok := s.Wait(10 * time.Second); !ok {
		t.Fatal("process did not exit after kill")
	}
	if _, ok := <-ch; ok {
		t.Fatal("reply after kill")
	}
	if s.Kill() {
		t.Fatal("second kill reported as landed")
	}
}

func TestStrayAndGarbageReplies(t *testing.T) {
	s, err := fake("garbage").StartServe("db", nil, clock())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(s.Stray()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	s.Kill()
	s.Wait(10 * time.Second)
	if got := s.Stray(); len(got) != 2 {
		t.Fatalf("stray replies: %v", got)
	}
}

func TestRunTimeout(t *testing.T) {
	r := fake("hang").Run(300*time.Millisecond, nil, "open", "x")
	if !r.TimedOut || r.OK() {
		t.Fatalf("hung command not timed out: %+v", r)
	}
	landed, _ := fake("hang").RunKillAfter(100 * time.Millisecond)
	if !landed {
		t.Fatal("kill of a running command did not land")
	}
}

func TestParseReply(t *testing.T) {
	var n atomic.Int32
	for _, c := range []struct {
		line string
		ok   bool
		kind string
	}{
		{"ACK a1", true, "ACK"},
		{"NOTFOUND a1", true, "NOTFOUND"},
		{`VALUE a1 "x y\n"`, true, "VALUE"},
		{"VALUE a1 plain", true, "VALUE"},
		{"ERR a1 full database is full", true, "ERR"},
		{"ACK", false, ""},
		{"ACK a1 extra", false, ""},
		{"WHAT a1", false, ""},
		{`VALUE a1 "unterminated`, false, ""},
	} {
		rp, err := ParseReply(c.line)
		if (err == nil) != c.ok || (c.ok && rp.Kind != c.kind) {
			t.Fatalf("%q: %+v %v", c.line, rp, err)
		}
		n.Add(1)
	}
	if rp, _ := ParseReply(`VALUE a1 "x y\n"`); string(rp.Value) != "x y\n" {
		t.Fatalf("unquote: %q", rp.Value)
	}
}
