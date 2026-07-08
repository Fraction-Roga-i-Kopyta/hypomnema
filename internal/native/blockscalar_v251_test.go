package native

import "testing"

func TestSplitFrontmatter_BlockScalarDoesNotClobberKeys(t *testing.T) { // review G2
	s := "---\n" +
		"name: real-name\n" +
		"description: THE REAL DESCRIPTION\n" +
		"root-cause: |\n" +
		"  first line of prose\n" +
		"  description: PROSE LINE MASQUERADING AS A KEY\n" +
		"  name: not-the-real-name\n" +
		"type: mistake\n" +
		"---\nbody\n"
	fm, _ := splitFrontmatter(s)
	if fm["description"] != "THE REAL DESCRIPTION" {
		t.Errorf("block-scalar prose overwrote description: got %q", fm["description"])
	}
	if fm["name"] != "real-name" {
		t.Errorf("block-scalar prose overwrote name: got %q", fm["name"])
	}
	if fm["type"] != "mistake" {
		t.Errorf("unindented key after the block scalar must still parse: got %q", fm["type"])
	}
}
