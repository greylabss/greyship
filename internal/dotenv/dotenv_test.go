package dotenv

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	src := "# comment\n" +
		"PLAIN=hello world\n" +
		"export EXPORTED=yes\n" +
		"SPACES = padded  \n" +
		"INLINE=value # a comment\n" +
		"HASH=abc#def\n" +
		"DOUBLE=\"line1\\nline2 \\\"q\\\"\"\n" +
		"SINGLE='$NOT_EXPANDED \\n'\n" +
		"EMPTY=\n" +
		"MULTI=\"-----BEGIN KEY-----\n" +
		"abc\n" +
		"-----END KEY-----\"\n" +
		"DUP=first\n" +
		"DUP=second\n"
	got, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"PLAIN": "hello world", "EXPORTED": "yes", "SPACES": "padded", "INLINE": "value", "HASH": "abc#def",
		"DOUBLE": "line1\nline2 \"q\"", "SINGLE": `$NOT_EXPANDED \n`, "EMPTY": "",
		"MULTI": "-----BEGIN KEY-----\nabc\n-----END KEY-----", "DUP": "second",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{"NOEQUALS\n", "1BAD=x\n", "K=\"never closed\n", "K='open\n"} {
		if _, err := Parse(src); err == nil {
			t.Errorf("Parse(%q) accepted bad input", src)
		}
	}
}

func TestFormatRoundTrips(t *testing.T) {
	in := map[string]string{"A": `quote " and \ backslash`, "B": "multi\nline\ttab", "C": "", "D": "#not a comment"}
	out, err := Parse(Format(in))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip changed values:\n%q\n%q", in, out)
	}
}
