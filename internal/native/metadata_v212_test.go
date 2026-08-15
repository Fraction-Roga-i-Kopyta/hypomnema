package native

import "testing"

// Claude Code's native memory instructions nest type/created/status under
// `metadata:`. hypomnema reads those children as if they were top-level.
func TestSplitFrontmatter_PromotesMetadataChildren(t *testing.T) {
	s := "---\n" +
		"name: nested\n" +
		"description: harness-shaped\n" +
		"metadata:\n" +
		"  node_type: memory\n" +
		"  type: mistake\n" +
		"  created: 2026-08-01\n" +
		"  status: candidate\n" +
		"  keywords: [alpha, beta]\n" +
		"  domains:\n" +
		"    - backend\n" +
		"    - devops\n" +
		"---\nbody\n"
	fm, body := splitFrontmatter(s)
	want := map[string]string{
		"name": "nested", "description": "harness-shaped",
		"type": "mistake", "created": "2026-08-01", "status": "candidate",
		"keywords": "[alpha, beta]", "domains": "backend, devops",
	}
	for k, v := range want {
		if fm[k] != v {
			t.Errorf("fm[%q] = %q, want %q", k, fm[k], v)
		}
	}
	if body != "body" {
		t.Errorf("body = %q, want %q", body, "body")
	}
}

// A column-0 key beats the same key nested under metadata, in either order.
func TestSplitFrontmatter_TopLevelWinsOverMetadata(t *testing.T) {
	before := "---\ntype: strategy\nmetadata:\n  type: mistake\n  status: active\n---\nx\n"
	after := "---\nmetadata:\n  type: mistake\n  status: active\ntype: strategy\n---\nx\n"
	for name, s := range map[string]string{"top-first": before, "nested-first": after} {
		fm, _ := splitFrontmatter(s)
		if fm["type"] != "strategy" {
			t.Errorf("%s: type = %q, want strategy (top-level wins)", name, fm["type"])
		}
		if fm["status"] != "active" {
			t.Errorf("%s: status = %q, want active (promoted, no top-level rival)", name, fm["status"])
		}
	}
}

// Only `metadata:` promotes. Any other empty-valued key followed by indented
// `k: v` lines keeps the old behaviour (indented lines skipped, review G2).
func TestSplitFrontmatter_OnlyMetadataPromotes(t *testing.T) {
	s := "---\nname: n\nextra:\n  type: mistake\n  status: pinned\n---\nx\n"
	fm, _ := splitFrontmatter(s)
	if fm["type"] != "" || fm["status"] != "" {
		t.Errorf("children of a non-metadata key must not promote: type=%q status=%q", fm["type"], fm["status"])
	}
}

// Deeper-indented lines inside metadata (a nested block scalar's prose) are
// continuation, never keys — the G2 protection holds one level down too.
func TestSplitFrontmatter_MetadataBlockScalarProseIsNotAKey(t *testing.T) {
	s := "---\n" +
		"name: real\n" +
		"metadata:\n" +
		"  type: mistake\n" +
		"  root-cause: |\n" +
		"    name: NOT-THE-NAME\n" +
		"    description: prose line\n" +
		"  status: active\n" +
		"---\nx\n"
	fm, _ := splitFrontmatter(s)
	if fm["name"] != "real" {
		t.Errorf("name clobbered by nested prose: %q", fm["name"])
	}
	if fm["description"] != "" {
		t.Errorf("description must not come from nested prose: %q", fm["description"])
	}
	if fm["type"] != "mistake" || fm["status"] != "active" {
		t.Errorf("siblings around the nested scalar must still promote: type=%q status=%q", fm["type"], fm["status"])
	}
}

// `metadata: ` with trailing whitespace (seen in the wild) still opens the block.
func TestSplitFrontmatter_MetadataTrailingSpace(t *testing.T) {
	s := "---\nmetadata: \n  type: knowledge\n---\nx\n"
	fm, _ := splitFrontmatter(s)
	if fm["type"] != "knowledge" {
		t.Errorf("type = %q, want knowledge", fm["type"])
	}
}

// A blank (or whitespace-only) line inside the metadata block is a no-op —
// it must not close the block and drop the children after it.
func TestSplitFrontmatter_BlankLineInsideMetadataIsNoop(t *testing.T) {
	s := "---\nmetadata:\n  type: knowledge\n\n   \n  status: pinned\n---\nx\n"
	fm, _ := splitFrontmatter(s)
	if fm["type"] != "knowledge" || fm["status"] != "pinned" {
		t.Errorf("blank line closed the block: type=%q status=%q", fm["type"], fm["status"])
	}
}

// Same rule for a top-level block list: a blank line between items no longer
// terminates the list (it used to reset the pending key; whitespace-only
// lines were already skipped without reset — the parser is now consistent
// and YAML-conformant).
func TestSplitFrontmatter_BlankLineInsideBlockListKeepsAccumulating(t *testing.T) {
	s := "---\nkeywords:\n  - a\n\n  - b\n---\nx\n"
	fm, _ := splitFrontmatter(s)
	if fm["keywords"] != "a, b" {
		t.Errorf("keywords = %q, want %q", fm["keywords"], "a, b")
	}
}
