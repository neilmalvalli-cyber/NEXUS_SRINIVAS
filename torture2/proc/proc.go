// Package proc runs the real NEXUS executable for the torture engine: one-shot
// CLI commands with timeouts, and `kvdb serve` sessions that can be fed
// requests, killed (TerminateProcess on Windows, SIGKILL elsewhere) and
// waited for. A reply counts only when its complete line, ending in '\n',
// has been read; a partial line left by a killed process promises nothing.
package proc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Binary is the kvdb executable under test.
type Binary struct {
	Path string
	Env  []string // extra environment variables (KEY=VALUE), e.g. for mutants
}

// Locate resolves path (default: kvdb next to this program, then ./kvdb)
// and proves it can be launched by running it with no arguments, which must
// print its usage and exit with status 2. A missing or unlaunchable binary is
// a clear error, never a silent pass.
func Locate(path string) (Binary, error) {
	name := "kvdb"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if path == "" {
		if exe, err := os.Executable(); err == nil {
			p := filepath.Join(filepath.Dir(exe), name)
			if _, err := os.Stat(p); err == nil {
				path = p
			}
		}
		if path == "" {
			path = name
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Binary{}, err
	}
	if fi, err := os.Stat(abs); err != nil {
		return Binary{}, fmt.Errorf("kvdb executable not found at %s (build it: go build -o . ./src/cmd/kvdb)", abs)
	} else if fi.IsDir() {
		return Binary{}, fmt.Errorf("%s is a directory, not the kvdb executable", abs)
	}
	b := Binary{Path: abs}
	r := b.Run(10*time.Second, nil)
	if r.StartErr != nil {
		return Binary{}, fmt.Errorf("cannot launch %s: %w", abs, r.StartErr)
	}
	if r.Exit != 2 || !strings.Contains(r.Stderr, "usage") {
		return Binary{}, fmt.Errorf("%s does not behave like kvdb (no-argument run: exit %d, stderr %q)", abs, r.Exit, trunc(r.Stderr, 120))
	}
	return b, nil
}

// Result is the outcome of a one-shot command.
type Result struct {
	Stdout, Stderr string
	Exit           int // -1 if it did not exit normally
	Dur            time.Duration
	StartErr       error // the process could not be started
	TimedOut       bool  // killed because it exceeded the timeout
}

// OK reports exit status 0.
func (r Result) OK() bool { return r.StartErr == nil && !r.TimedOut && r.Exit == 0 }

func (r Result) String() string {
	switch {
	case r.StartErr != nil:
		return "could not start: " + r.StartErr.Error()
	case r.TimedOut:
		return fmt.Sprintf("timed out after %s; stderr: %s", r.Dur.Round(time.Millisecond), trunc(r.Stderr, 300))
	}
	return fmt.Sprintf("exit %d; stdout: %s; stderr: %s", r.Exit, trunc(r.Stdout, 300), trunc(r.Stderr, 300))
}

func (b Binary) command(args []string) *exec.Cmd {
	c := exec.Command(b.Path, args...)
	if len(b.Env) > 0 {
		c.Env = append(os.Environ(), b.Env...)
	}
	return c
}

// Run runs kvdb with args, killing it if it exceeds timeout. stdin, if not
// nil, is fed to the process.
func (b Binary) Run(timeout time.Duration, stdin []byte, args ...string) Result {
	c := b.command(args)
	var o, e bytes.Buffer
	c.Stdout, c.Stderr = &o, &e
	if stdin != nil {
		c.Stdin = bytes.NewReader(stdin)
	}
	t0 := time.Now()
	if err := c.Start(); err != nil {
		return Result{Exit: -1, StartErr: err}
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	var r Result
	select {
	case err := <-done:
		r.Exit = exitCode(err)
	case <-time.After(timeout):
		_ = c.Process.Kill()
		<-done
		r.TimedOut, r.Exit = true, -1
	}
	r.Dur = time.Since(t0)
	r.Stdout, r.Stderr = o.String(), e.String()
	return r
}

// RunKillAfter starts kvdb with args and kills it after delay. landed is true
// if the process was still running when the kill was sent.
func (b Binary) RunKillAfter(delay time.Duration, args ...string) (landed bool, r Result) {
	c := b.command(args)
	var o, e bytes.Buffer
	c.Stdout, c.Stderr = &o, &e
	t0 := time.Now()
	if err := c.Start(); err != nil {
		return false, Result{Exit: -1, StartErr: err}
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		r.Exit = exitCode(err)
	case <-time.After(delay):
		landed = c.Process.Kill() == nil
		err := <-done
		r.Exit = exitCode(err)
	}
	r.Dur = time.Since(t0)
	r.Stdout, r.Stderr = o.String(), e.String()
	return landed, r
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// Reply is one complete line printed by serve.
type Reply struct {
	Kind  string // ACK, VALUE, NOTFOUND, ERR
	ID    string
	Class string // ERR only
	Text  string
	Value []byte // VALUE only
	At    int64  // clock time when the complete line was read
}

// Session is a running `kvdb serve`.
type Session struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	clock   func() int64
	started time.Time

	wmu         sync.Mutex // serializes writes to stdin
	inputClosed bool       // set by CloseInput, under wmu
	mu          sync.Mutex
	wait        map[string]chan Reply
	closed      bool // stdout reached EOF: no more replies
	stray       []string
	ready       chan struct{}

	stderr  syncBuf
	readEOF chan struct{}
	exited  chan struct{}
	exitErr error
}

// StartServe launches `kvdb serve db extra...`. clock stamps reply times
// (the same clock the caller uses for send times).
func (b Binary) StartServe(db string, extra []string, clock func() int64) (*Session, error) {
	c := b.command(append([]string{"serve", db}, extra...))
	stdin, err := c.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		return nil, err
	}
	s := &Session{cmd: c, stdin: stdin, clock: clock, wait: map[string]chan Reply{},
		ready: make(chan struct{}), readEOF: make(chan struct{}), exited: make(chan struct{})}
	s.stderr.ready = s.ready
	c.Stderr = &s.stderr
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("cannot launch %s serve: %w", b.Path, err)
	}
	s.started = time.Now()
	go s.readLoop(stdout)
	go func() {
		<-s.readEOF
		s.exitErr = c.Wait()
		close(s.exited)
	}()
	return s, nil
}

// Pid returns the process ID.
func (s *Session) Pid() int { return s.cmd.Process.Pid }

// WaitReady waits for serve's "ready" line on stderr (recovery finished).
func (s *Session) WaitReady(timeout time.Duration) bool {
	select {
	case <-s.ready:
		return true
	case <-s.exited:
		return false
	case <-time.After(timeout):
		return false
	}
}

// Send registers op id and writes payload (one or more complete lines). The
// returned channel delivers the reply, or is closed without one if the
// process's output ended first.
// ErrInputClosed is returned by Send when CloseInput already ran: no byte of
// the request reached the process, so it definitely did not take effect.
var ErrInputClosed = errors.New("serve input already closed; request not sent")

func (s *Session) Send(id string, payload []byte) (<-chan Reply, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("serve output already ended")
	}
	ch := make(chan Reply, 1)
	s.wait[id] = ch
	s.mu.Unlock()
	s.wmu.Lock()
	if s.inputClosed {
		s.wmu.Unlock()
		s.mu.Lock()
		delete(s.wait, id)
		s.mu.Unlock()
		return nil, ErrInputClosed // nothing was written
	}
	_, err := s.stdin.Write(payload)
	s.wmu.Unlock()
	if err != nil {
		// The request may still have been read in part or whole before the
		// pipe broke; the caller treats it as unknown. Keep the channel so a
		// reply that does arrive is still delivered.
		return ch, err
	}
	return ch, nil
}

// CloseInput closes serve's stdin: a graceful shutdown request.
func (s *Session) CloseInput() error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.inputClosed = true
	return s.stdin.Close()
}

// Kill terminates the process. landed is true if it was still running.
func (s *Session) Kill() (landed bool) {
	select {
	case <-s.exited:
		return false
	default:
	}
	return s.cmd.Process.Kill() == nil
}

// Wait waits for the process to exit (after its output ended).
func (s *Session) Wait(timeout time.Duration) (exit int, ok bool) {
	select {
	case <-s.exited:
		return exitCode(s.exitErr), true
	case <-time.After(timeout):
		return -1, false
	}
}

// Exited reports whether the process has exited.
func (s *Session) Exited() bool {
	select {
	case <-s.exited:
		return true
	default:
		return false
	}
}

// Stderr returns what the process printed on stderr.
func (s *Session) Stderr() string { return s.stderr.String() }

// Stray returns reply lines that matched no waiting request, or could not be
// parsed.
func (s *Session) Stray() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.stray...)
}

// Release closes stdin after the process is gone.
func (s *Session) Release() { _ = s.CloseInput() }

func (s *Session) readLoop(out io.Reader) {
	br := bufio.NewReaderSize(out, 1<<20)
	defer func() {
		s.mu.Lock()
		s.closed = true
		for id, ch := range s.wait {
			close(ch)
			delete(s.wait, id)
		}
		s.mu.Unlock()
		close(s.readEOF)
	}()
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			// EOF or a broken pipe: a final line without '\n' is incomplete
			// and therefore not a reply.
			return
		}
		at := s.clock()
		line = strings.TrimRight(line, "\r\n")
		rp, perr := ParseReply(line)
		if perr != nil {
			s.mu.Lock()
			s.stray = append(s.stray, perr.Error())
			s.mu.Unlock()
			continue
		}
		rp.At = at
		s.mu.Lock()
		ch, ok := s.wait[rp.ID]
		if ok {
			delete(s.wait, rp.ID)
		} else {
			s.stray = append(s.stray, "reply for unknown or already-answered op: "+trunc(line, 100))
		}
		s.mu.Unlock()
		if ok {
			ch <- rp
		}
	}
}

// ParseReply parses one complete serve output line.
func ParseReply(line string) (Reply, error) {
	kind, rest, _ := strings.Cut(line, " ")
	id, rest, _ := strings.Cut(rest, " ")
	rp := Reply{Kind: kind, ID: id}
	if id == "" {
		return rp, fmt.Errorf("unparsable reply %q", trunc(line, 100))
	}
	switch kind {
	case "ACK", "NOTFOUND":
		if rest != "" {
			return rp, fmt.Errorf("unparsable reply %q", trunc(line, 100))
		}
	case "VALUE":
		v := rest
		if strings.HasPrefix(v, `"`) {
			u, err := strconv.Unquote(v)
			if err != nil {
				return rp, fmt.Errorf("bad quoted value in %q", trunc(line, 100))
			}
			v = u
		}
		rp.Value = []byte(v)
	case "ERR":
		rp.Class, rp.Text, _ = strings.Cut(rest, " ")
	default:
		return rp, fmt.Errorf("unparsable reply %q", trunc(line, 100))
	}
	return rp, nil
}

// syncBuf is a goroutine-safe stderr buffer that also signals serve's ready line.
type syncBuf struct {
	mu    sync.Mutex
	b     bytes.Buffer
	ready chan struct{}
	fired bool
}

func (w *syncBuf) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.b.Len() < 1<<20 {
		w.b.Write(p)
	}
	if !w.fired && bytes.Contains(w.b.Bytes(), []byte("serve: ready")) {
		w.fired = true
		close(w.ready)
	}
	return len(p), nil
}

func (w *syncBuf) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
