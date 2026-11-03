package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

func runeWidth(r rune) int {
	w := runewidth.RuneWidth(r)
	if w < 0 {
		return 0
	}
	return w
}

type lineEditor struct {
	buf   []rune
	cells int
}

func (e *lineEditor) insert(r rune) []byte {
	e.buf = append(e.buf, r)
	e.cells += runeWidth(r)
	return []byte(string(r))
}

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

func (e *lineEditor) line() string {
	return string(e.buf)
}

func plainReadLine(tty *os.File, prompt string) (string, error) {
	oldState, err := term.MakeRaw(int(tty.Fd()))
	if err != nil {

		return readLineScanner(tty)
	}
	defer term.Restore(int(tty.Fd()), oldState)

	io.WriteString(tty, prompt)

	e := &lineEditor{}
	var pending []byte
	var assembling []byte
	var ok bool
	for {

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
		case 0x7f, 0x08:
			if seq := e.backspace(); seq != nil {
				tty.Write(seq)
			}
		case 0x15:
			tty.Write(e.clearLine())
		case 0x03:
			term.Restore(int(tty.Fd()), oldState)
			io.WriteString(tty, "^C\r\n")
			os.Exit(130)
		case 0x04:
			if len(e.buf) == 0 {
				io.WriteString(tty, "\r\n")
				return "", io.EOF
			}

			io.WriteString(tty, "\r\n")
			return e.line(), nil
		default:

			if b < 0x20 && b != '\t' {
				continue
			}

			if b < 0x80 {
				e.insert(rune(b))
				tty.Write([]byte{b})
				continue
			}

			var r rune
			assembling, r, ok = feedAssembler(assembling, b)
			if ok {
				tty.Write(e.insert(r))
			}
		}
	}
}

func feedAssembler(acc []byte, b byte) (next []byte, r rune, emit bool) {
	if len(acc) == 0 {
		switch {
		case b&0xE0 == 0xC0, b&0xF0 == 0xE0, b&0xF8 == 0xF0:
			return []byte{b}, 0, false
		default:
			return nil, 0, false
		}
	}

	if b&0xC0 != 0x80 {

		if b&0xE0 == 0xC0 || b&0xF0 == 0xE0 || b&0xF8 == 0xF0 {
			return []byte{b}, 0, false
		}
		return nil, 0, false
	}
	acc = append(acc, b)
	r, size := utf8.DecodeRune(acc)
	switch {
	case r != utf8.RuneError && size == len(acc):
		return nil, r, true
	case len(acc) >= 4:
		return nil, 0, false
	default:
		return acc, 0, false
	}
}

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
