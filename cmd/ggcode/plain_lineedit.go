package main

// plain_lineedit.go: rune-aware line editor for the --plain interactive REPL.
//
// Why this exists (#plain-cjk): the linear REPL used to read stdin through a
// bufio.Scanner, which leaves line editing to the terminal's cooked-mode line
// discipline. That discipline erases ONE BYTE per backspace; a CJK rune is
// 3 bytes / 2 display columns, so backspacing Chinese text left screen
// residue (one column per backspace, invisible byte garbage in the buffer).
// Linux can fix this kernel-side with termios IUTF8; macOS has no such flag,
// so the only portable cure is owning the line ourselves: raw mode plus a
// rune-aware editor that erases wcwidth(r) columns per backspace.
//
// Scope is deliberately minimal (linear append model): printable rune insert,
// backspace (rune-granular, width-aware), Ctrl-U clear-line, Ctrl-C
// interrupt, Ctrl-D/EOT submit-or-exit. No cursor motion, no history - the
// plain REPL contract is "one line in, one answer out".

import (
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

// runeWidth returns the terminal cell count of r. Combining marks and
// zero-width joiners occupy no cells; CJK occupy two; the rest follow
// runewidth's East-Asian-width tables.
func runeWidth(r rune) int {
	w := runewidth.RuneWidth(r)
	if w < 0 {
		return 0
	}
	return w
}

// lineEditor is the testable core of the interactive line editor: a rune
// buffer plus the echo already emitted for it. All state transitions are
// pure functions over runes so the UTF-8 boundaries are unit-testable
// without a tty.
type lineEditor struct {
	buf   []rune
	cells int // display cells the current buffer occupies
}

// insert appends r and returns the bytes to echo (r's UTF-8 encoding).
func (e *lineEditor) insert(r rune) []byte {
	e.buf = append(e.buf, r)
	e.cells += runeWidth(r)
	return []byte(string(r))
}

// backspace removes the last rune. It returns the terminal sequence that
// erases that rune's cells (\b + spaces + \b, one space per cell), or nil
// when the buffer is empty (caller must NOT emit an erase then - doing so
// would eat into the prompt).
func (e *lineEditor) backspace() []byte {
	if len(e.buf) == 0 {
		return nil
	}
	w := runeWidth(e.buf[len(e.buf)-1])
	e.buf = e.buf[:len(e.buf)-1]
	e.cells -= w
	seq := make([]byte, 0, 2*w+2)
	for i := 0; i < w; i++ {
		seq = append(seq, '\b', ' ')
	}
	for i := 0; i < w; i++ {
		seq = append(seq, '\b')
	}
	return seq
}

// clearLine wipes the whole buffer (Ctrl-U): backspaces cells then erases.
func (e *lineEditor) clearLine() []byte {
	seq := make([]byte, 0, 4*e.cells+2)
	for i := 0; i < e.cells; i++ {
		seq = append(seq, '\b', ' ')
	}
	for i := 0; i < e.cells; i++ {
		seq = append(seq, '\b')
	}
	e.buf = e.buf[:0]
	e.cells = 0
	return seq
}

// line returns the buffer as a string.
func (e *lineEditor) line() string {
	return string(e.buf)
}

// plainReadLine runs one interactive raw-mode read on tty. It restores the
// terminal on every exit path. Returns (line, nil) on Enter/Ctrl-D-with-
// content, ("", io.EOF) on Ctrl-D at an empty line (REPL exit), and
// ("", err) on hard IO errors. Ctrl-C restores the terminal and terminates
// the process with status 130, matching the cooked-mode default SIGINT the
// Scanner-based loop used to get for free.
func plainReadLine(tty *os.File, prompt string) (string, error) {
	oldState, err := term.MakeRaw(int(tty.Fd()))
	if err != nil {
		// Raw mode unavailable (weird tty): degrade to Scanner semantics.
		return readLineScanner(tty)
	}
	defer term.Restore(int(tty.Fd()), oldState)

	// Raw mode disables ONLCR echo-newline handling too; emit \r\n ourselves.
	io.WriteString(tty, prompt)

	e := &lineEditor{}
	var pending []byte
	for {
		// Drain any bytes the terminal handed us; refill in 1-byte-passes
		// through a small buffer when empty.
		if len(pending) == 0 {
			var one [1]byte
			n, err := tty.Read(one[:])
			if err != nil {
				return "", err
			}
			if n == 0 {
				continue
			}
			pending = one[:]
		}

		b := pending[0]
		pending = pending[1:]

		switch b {
		case '\r', '\n':
			io.WriteString(tty, "\r\n")
			return e.line(), nil
		case 0x7f, 0x08: // backspace / Ctrl-H
			if seq := e.backspace(); seq != nil {
				tty.Write(seq)
			}
		case 0x15: // Ctrl-U: clear line
			tty.Write(e.clearLine())
		case 0x03: // Ctrl-C: restore + exit like a terminal interrupt
			term.Restore(int(tty.Fd()), oldState)
			io.WriteString(tty, "^C\r\n")
			os.Exit(130)
		case 0x04: // Ctrl-D: EOF - exit at empty line, submit otherwise
			if len(e.buf) == 0 {
				io.WriteString(tty, "\r\n")
				return "", io.EOF
			}
			// Deliberate asymmetry with backspace: Ctrl-D on content acts
			// as "submit what's typed" (terminal convention), not delete.
			io.WriteString(tty, "\r\n")
			return e.line(), nil
		default:
			// Control chars other than TAB are swallowed (raw mode exposes
			// them all); TAB and everything printable-decodable go through
			// the UTF-8 assembler.
			if b < 0x20 && b != '\t' {
				continue
			}
			// Assemble one rune. Fast path: single-byte ASCII.
			if b < 0x80 {
				e.insert(rune(b))
				tty.Write([]byte{b})
				continue
			}
			// Multi-byte rune: the 1-byte refill above means pending is empty
			// once b is consumed, so continuation bytes must be accumulated
			// explicitly or the leader is lost (#4940).
			r, ok, err := readFullRune(tty, b, &pending)
			if err != nil {
				return "", err
			}
			if !ok {
				continue // stray continuation or malformed sequence: drop silently
			}
			tty.Write(e.insert(r))
		}
	}
}

// utf8Need returns how many continuation bytes the UTF-8 leader b expects
// (0 for a stray continuation byte).
func utf8Need(b byte) int {
	switch {
	case b&0xE0 == 0xC0:
		return 1
	case b&0xF0 == 0xE0:
		return 2
	case b&0xF8 == 0xF0:
		return 3
	}
	return 0
}

// readFullRune accumulates the continuation bytes for leader b through the
// same 1-byte refill semantics as the caller (draining *pending first), then
// decodes the full sequence. ok=false means the sequence was invalid and
// must be dropped silently.
func readFullRune(rd io.Reader, b byte, pending *[]byte) (r rune, ok bool, err error) {
	need := utf8Need(b)
	if need == 0 {
		return 0, false, nil // stray continuation byte
	}
	full := make([]byte, 0, need+1)
	full = append(full, b)
	for len(full) < need+1 {
		if len(*pending) == 0 {
			var one [1]byte
			n, rerr := rd.Read(one[:])
			if rerr != nil {
				return 0, false, rerr
			}
			if n == 0 {
				continue
			}
			*pending = one[:]
		}
		full = append(full, (*pending)[0])
		*pending = (*pending)[1:]
	}
	r, size := utf8.DecodeRune(full)
	if r == utf8.RuneError && size <= 1 {
		return 0, false, nil // malformed sequence
	}
	return r, true, nil
}

// assembleRune decodes one rune given the leading byte b and the remaining
// continuation bytes avail. Returns (rune, utf8Size, bytesConsumedFromAvail).
// size==0 means "need more bytes".
func assembleRune(avail []byte, b byte) (r rune, size int, consumed int) {
	need := utf8Need(b)
	if need == 0 {
		return utf8.RuneError, 1, 0 // stray continuation byte: drop it
	}
	if len(avail) < need {
		return 0, 0, 0 // incomplete: wait
	}
	full := append([]byte{b}, avail[:need]...)
	r, size = utf8.DecodeRune(full)
	if size != need+1 {
		// Malformed sequence: drop just the leader.
		return utf8.RuneError, 1, 0
	}
	return r, size, need
}

// readLineScanner is the fallback when MakeRaw fails: cooked-mode line read
// (original Scanner behavior, byte-erase residue and all).
func readLineScanner(r io.Reader) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			c := buf[0]
			if c == '\n' {
				return sb.String(), nil
			}
			if c != '\r' {
				sb.WriteByte(c)
			}
		}
		if err != nil {
			if sb.Len() > 0 && errors.Is(err, io.EOF) {
				return sb.String(), nil
			}
			return sb.String(), err
		}
	}
}
