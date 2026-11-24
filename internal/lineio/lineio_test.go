package lineio

import (
	"bufio"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

func collect(t *testing.T, r io.Reader) []string {
	t.Helper()
	var got []string
	if err := Each(r, func(line string) error {
		got = append(got, line)
		return nil
	}); err != nil {
		t.Fatalf("Each: %v", err)
	}
	return got
}

// scanLines is the bufio.ScanLines reference the helper must match on
// inputs short enough for the default Scanner.
func scanLines(s string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

func TestEach_LineOverOneMiBThenNormalLines(t *testing.T) {
	huge := strings.Repeat("x", 3<<20) // 3 MiB, well past any Scanner cap
	in := "2026-01-01|inject|a|s1\n" + huge + "\n2026-01-02|inject|b|s2\n2026-01-03|inject|c|s3\n"
	got := collect(t, strings.NewReader(in))
	if len(got) != 4 {
		t.Fatalf("got %d lines, want 4", len(got))
	}
	if got[1] != huge {
		t.Fatalf("huge line truncated: len %d, want %d", len(got[1]), len(huge))
	}
	want := []string{"2026-01-02|inject|b|s2", "2026-01-03|inject|c|s3"}
	if !reflect.DeepEqual(got[2:], want) {
		t.Fatalf("lines after huge row = %q, want %q", got[2:], want)
	}
}

func TestEach_MatchesScanLines(t *testing.T) {
	cases := []string{
		"",
		"a",
		"a\n",
		"a\nb",
		"a\r\nb\r\n",
		"a\r\nb",
		"a\r\r\n",
		"\n\n",
		"a\n\nb\n",
		"\r",
		"a\r",
		"only\rinner\n",
	}
	for _, in := range cases {
		got := collect(t, strings.NewReader(in))
		want := scanLines(in)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Each(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEach_CRLF(t *testing.T) {
	got := collect(t, strings.NewReader("one\r\ntwo\r\nthree"))
	want := []string{"one", "two", "three"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEach_NoTrailingNewline(t *testing.T) {
	got := collect(t, strings.NewReader("first\nlast"))
	want := []string{"first", "last"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEach_EmptyInput(t *testing.T) {
	if got := collect(t, strings.NewReader("")); len(got) != 0 {
		t.Fatalf("got %q, want no lines", got)
	}
}

func TestEach_ReadErrorIsReturned(t *testing.T) {
	boom := errors.New("boom")
	r := io.MultiReader(strings.NewReader("a\nb\n"), iotest.ErrReader(boom))
	var got []string
	err := Each(r, func(line string) error { got = append(got, line); return nil })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("lines before the error = %q, want [a b]", got)
	}
}

func TestEach_StopEndsWithoutError(t *testing.T) {
	var got []string
	err := Each(strings.NewReader("a\nb\nc\n"), func(line string) error {
		got = append(got, line)
		if line == "b" {
			return ErrStop
		}
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("got %q, want [a b]", got)
	}
}

func TestEach_CallbackErrorIsReturned(t *testing.T) {
	boom := errors.New("callback")
	n := 0
	err := Each(strings.NewReader("a\nb\n"), func(string) error { n++; return boom })
	if !errors.Is(err, boom) || n != 1 {
		t.Fatalf("err = %v after %d calls, want %v after 1", err, n, boom)
	}
}

func TestEach_NilArgs(t *testing.T) {
	if err := Each(nil, func(string) error { return nil }); err == nil {
		t.Fatal("nil reader: want error")
	}
	if err := Each(strings.NewReader("a\n"), nil); err == nil {
		t.Fatal("nil fn: want error")
	}
}
