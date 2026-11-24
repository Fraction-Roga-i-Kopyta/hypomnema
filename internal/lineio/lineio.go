// Package lineio reads newline-delimited text without a line-length cap.
//
// bufio.Scanner stops at the first line longer than its buffer cap:
// Scan returns false with bufio.ErrTooLong, and every later line is lost.
// For the WAL — the source of truth — one oversized row would silently
// truncate every reader built on a Scanner. Each has no such cap.
package lineio

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// ErrStop, returned by the callback, ends Each early without an error.
var ErrStop = errors.New("lineio: stop")

// Each calls fn with every line of r, in order. Line semantics match
// bufio.ScanLines: the trailing "\n" and then one trailing "\r" are
// stripped, and a final line without a newline is still delivered (empty
// input yields no lines). There is no line-length limit.
//
// Each returns nil at EOF or when fn returns ErrStop; fn's other errors and
// read errors are returned as-is. Lines read before a read error have
// already been delivered. A nil r or fn is an error.
func Each(r io.Reader, fn func(line string) error) error {
	if r == nil {
		return errors.New("lineio: nil reader")
	}
	if fn == nil {
		return errors.New("lineio: nil callback")
	}
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		s, err := br.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		if s != "" {
			s = strings.TrimSuffix(s, "\n")
			s = strings.TrimSuffix(s, "\r")
			if ferr := fn(s); ferr != nil {
				if errors.Is(ferr, ErrStop) {
					return nil
				}
				return ferr
			}
		}
		if err == io.EOF {
			return nil
		}
	}
}
