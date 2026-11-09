package native

import (
	"strings"
	"testing"
)

func TestSetStatus_TopLevel(t *testing.T) {
	in := "---\ntype: note\nstatus: active\nname: x\n---\nbody status: active\n"
	got, err := SetStatus(in, "pinned")
	if err != nil {
		t.Fatal(err)
	}
	want := "---\ntype: note\nstatus: pinned\nname: x\n---\nbody status: active\n"
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestSetStatus_NestedMetadata(t *testing.T) {
	in := "---\nname: x\nmetadata:\n  type: note\n  status: candidate\n  originSessionId: abc\n---\nb\n"
	got, err := SetStatus(in, "pinned")
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nname: x\nmetadata:\n  type: note\n  status: pinned\n  originSessionId: abc\n---\nb\n"
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestSetStatus_Both(t *testing.T) {
	in := "---\nstatus: active\nmetadata:\n  status: candidate\n---\nb\n"
	got, _ := SetStatus(in, "pinned")
	if strings.Count(got, "status: pinned") != 2 || strings.Contains(got, "candidate") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestSetStatus_Insert(t *testing.T) {
	in := "---\nname: x\n---\nb\n"
	got, err := SetStatus(in, "pinned")
	if err != nil {
		t.Fatal(err)
	}
	if got != "---\nname: x\nstatus: pinned\n---\nb\n" {
		t.Fatalf("got:\n%s", got)
	}
}

func TestSetStatus_BOM(t *testing.T) {
	in := "\xef\xbb\xbf---\nname: x\nstatus: active\n---\nb\n"
	got, err := SetStatus(in, "pinned")
	if err != nil {
		t.Fatal(err)
	}
	if got != "\xef\xbb\xbf---\nname: x\nstatus: pinned\n---\nb\n" {
		t.Fatalf("got:\n%q", got)
	}
}

func TestSetStatus_NoFrontmatter(t *testing.T) {
	if _, err := SetStatus("just body\n", "pinned"); err == nil {
		t.Fatal("want error")
	}
}

func TestBodySHA_IgnoresStatus(t *testing.T) {
	a := "---\nname: x\nstatus: active\nmetadata:\n  status: candidate\n---\nbody\n"
	b := "---\nname: x\nstatus: pinned\nmetadata:\n  status: pinned\n---\nbody\n"
	c := "---\nname: x\nstatus: active\n---\nbody2\n"
	bomA := "\xef\xbb\xbf---\nname: x\nstatus: active\n---\nbody\n"
	bomB := "\xef\xbb\xbf---\nname: x\nstatus: pinned\n---\nbody\n"
	if BodySHA(bomA) != BodySHA(bomB) {
		t.Fatal("status must not affect sha for BOM files")
	}
	if BodySHA(a) != BodySHA(b) {
		t.Fatal("status must not affect sha")
	}
	if BodySHA(a) == BodySHA(c) || len(BodySHA(a)) != 12 {
		t.Fatal("body change must change sha; len 12")
	}
}
