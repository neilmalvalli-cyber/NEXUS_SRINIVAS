package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

// MaxLine is the longest input line accepted by `batch` files and `serve`:
// a put of the largest key and value plus command words and IDs
// (DESIGN §18.4: the default 64 KiB scanner limit is too small for 1 MiB
// values).
const MaxLine = kvdb.MaxValueLen + kvdb.MaxKeyLen + 4096

var errLineTooLong = errors.New("line too long")

// lineReader reads '\n'-terminated lines of at most MaxLine bytes. It strips
// one trailing '\r' (Windows CRLF input). An over-long line is consumed
// completely and reported as errLineTooLong, so the next call starts on the
// following line.
type lineReader struct {
	r    *bufio.Reader
	line int // number of the line last returned (1-based)
	// requireEOL rejects a final line that has no terminating newline
	// (errIncompleteLine). serve sets it: such a line is a request whose
	// sender stopped mid-write, and must never be executed.
	requireEOL bool
}

var errIncompleteLine = errors.New("incomplete final line (no newline): not executed")

func newLineReader(r io.Reader) *lineReader {
	return &lineReader{r: bufio.NewReaderSize(r, 64<<10)}
}

// next returns the next line without its terminator. At end of input it
// returns io.EOF; a final line without '\n' is returned normally first.
func (lr *lineReader) next() (string, error) {
	var buf []byte
	tooLong := false
	for {
		chunk, err := lr.r.ReadSlice('\n')
		if !tooLong {
			if len(buf)+len(chunk) > MaxLine+2 {
				tooLong, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		switch {
		case err == nil:
			lr.line++
			if tooLong {
				return "", fmt.Errorf("line %d: %w (max %d bytes)", lr.line, errLineTooLong, MaxLine)
			}
			return trimEOL(buf), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(buf) == 0 && !tooLong {
				return "", io.EOF
			}
			lr.line++
			if tooLong {
				return "", fmt.Errorf("line %d: %w (max %d bytes)", lr.line, errLineTooLong, MaxLine)
			}
			if lr.requireEOL {
				return "", fmt.Errorf("line %d: %w", lr.line, errIncompleteLine)
			}
			return trimEOL(buf), nil
		default:
			return "", err
		}
	}
}

func trimEOL(b []byte) string {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	if n := len(b); n > 0 && b[n-1] == '\r' {
		b = b[:n-1]
	}
	return string(b)
}

// cutWord splits s at the first space: the word before it and the rest after
// it. Leading spaces are skipped first. The rest keeps everything after the
// single separating space, so a value may itself start with or contain spaces.
func cutWord(s string) (word, rest string) {
	s = strings.TrimLeft(s, " ")
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// parseOp parses one batch operation: "put <key> <value>" or "del <key>".
// For put, the value is the remainder of the line after the single space
// that follows the key; it may contain spaces and may be empty ("put k" or
// "put k " store an empty value, DESIGN §18.4). Keys cannot contain spaces in
// this text form.
func parseOp(line string) (kvdb.Op, error) {
	cmd, rest := cutWord(line)
	switch strings.ToLower(cmd) {
	case "put":
		key, value := cutWord(rest)
		if key == "" {
			return kvdb.Op{}, errors.New(`"put" needs a key`)
		}
		op := kvdb.Op{Kind: kvdb.OpPut, Key: []byte(key), Value: []byte(value)}
		return op, op.Validate()
	case "del", "delete":
		key, extra := cutWord(rest)
		if key == "" {
			return kvdb.Op{}, errors.New(`"del" needs a key`)
		}
		if strings.TrimSpace(extra) != "" {
			return kvdb.Op{}, fmt.Errorf(`"del %s" has unexpected text after the key`, key)
		}
		op := kvdb.Op{Kind: kvdb.OpDelete, Key: []byte(key)}
		return op, op.Validate()
	case "":
		return kvdb.Op{}, errors.New("empty operation")
	default:
		return kvdb.Op{}, fmt.Errorf("unknown operation %q (want put or del)", cmd)
	}
}

// readBatchFile parses a batch file: one operation per line, blank lines
// ignored. It stops with an error as soon as the file holds more than
// kvdb.MaxBatchOps operations, before allocating for the rest.
func readBatchFile(r io.Reader) ([]kvdb.Op, error) {
	lr := newLineReader(r)
	var ops []kvdb.Op
	for {
		line, err := lr.next()
		if errors.Is(err, io.EOF) {
			return ops, nil
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(ops) == kvdb.MaxBatchOps {
			return nil, fmt.Errorf("line %d: %w: more than %d operations in one batch", lr.line, kvdb.ErrTooLarge, kvdb.MaxBatchOps)
		}
		op, err := parseOp(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lr.line, err)
		}
		ops = append(ops, op)
	}
}

// plainKey reports whether key can be printed unquoted in dump output:
// printable ASCII without spaces, not starting with a double quote.
func plainKey(k []byte) bool {
	if len(k) == 0 || k[0] == '"' {
		return false
	}
	for _, c := range k {
		if c <= ' ' || c >= 0x7f {
			return false
		}
	}
	return true
}

// plainValue reports whether value can be printed unquoted: valid UTF-8 with
// no control characters, and not starting with a double quote.
func plainValue(v []byte) bool {
	if len(v) > 0 && v[0] == '"' {
		return false
	}
	if !utf8.Valid(v) {
		return false
	}
	for _, c := range v {
		if c < ' ' || c == 0x7f {
			return false
		}
	}
	return true
}

// formatPair renders one dump line. When key and value are both plain it is
// "<key> <value>" (the key has no spaces, so everything after the first
// space is the value). Otherwise both are written as Go-quoted strings:
// "\"<key>\" \"<value>\"". A line starting with '"' is always the quoted form.
func formatPair(k, v []byte) string {
	if plainKey(k) && plainValue(v) {
		return string(k) + " " + string(v)
	}
	return strconv.Quote(string(k)) + " " + strconv.Quote(string(v))
}

// formatValue renders a value for `get` and the serve VALUE reply: as is
// when plain, Go-quoted otherwise.
func formatValue(v []byte) string {
	if plainValue(v) {
		return string(v)
	}
	return strconv.Quote(string(v))
}
