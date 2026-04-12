// Mgmt
// Copyright (C) James Shubin and the project contributors
// Written by James Shubin <james@shubin.ca> and the project contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//
// Additional permission under GNU GPL version 3 section 7
//
// If you modify this program, or any covered work, by linking or combining it
// with embedded mcl code and modules (and that the embedded mcl code and
// modules which link with this program, contain a copy of their source code in
// the authoritative form) containing parts covered by the terms of any other
// license, the licensors of this program grant you additional permission to
// convey the resulting work. Furthermore, the licensors of this program grant
// the original author, James Shubin, additional permission to update this
// additional permission if he deems it necessary to achieve the goals of this
// additional permission.

package format

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/purpleidea/mgmt/lang/frontend/syntax"
	langtoken "github.com/purpleidea/mgmt/lang/frontend/token"
)

func TestGoldenFixtures(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "*.in.mcl"))
	if err != nil {
		t.Fatalf("failed to enumerate fixtures: %+v", err)
	}
	if len(inputs) == 0 {
		t.Fatalf("expected at least one formatter fixture")
	}

	for _, inputPath := range inputs {
		name := strings.TrimSuffix(filepath.Base(inputPath), ".in.mcl")
		t.Run(name, func(t *testing.T) {
			input, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatalf("failed to read input fixture: %+v", err)
			}
			golden, err := os.ReadFile(filepath.Join("testdata", name+".golden.mcl"))
			if err != nil {
				t.Fatalf("failed to read golden fixture: %+v", err)
			}

			got := formatSourceForTest(t, name+".mcl", input)
			if string(got) != string(golden) {
				t.Fatalf("formatted output mismatch\nwant:\n%s\ngot:\n%s", golden, got)
			}
		})
	}
}

func TestGoldenFixturesAreIdempotent(t *testing.T) {
	goldens, err := filepath.Glob(filepath.Join("testdata", "*.golden.mcl"))
	if err != nil {
		t.Fatalf("failed to enumerate goldens: %+v", err)
	}

	for _, goldenPath := range goldens {
		name := filepath.Base(goldenPath)
		t.Run(name, func(t *testing.T) {
			golden, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("failed to read golden: %+v", err)
			}

			got := formatSourceForTest(t, name, golden)
			if string(got) != string(golden) {
				t.Fatalf("formatter is not idempotent\nwant:\n%s\ngot:\n%s", golden, got)
			}
		})
	}
}

func TestCommentsArePreserved(t *testing.T) {
	src := []byte("# before\nfile \"/tmp/x\" { # resource\n\tcontent => \"hello\",\t# inline entry\n\n\t# inside block\n\tMeta:hidden => false,\n}\n")

	got := formatSourceForTest(t, "comments.mcl", src)

	if want, gotComments := collectComments(src), collectComments(got); !slices.Equal(want, gotComments) {
		t.Fatalf("comment raws changed\nwant: %v\ngot:  %v", want, gotComments)
	}
}

func TestStringsArePreserved(t *testing.T) {
	src := []byte("$template = \"hello ${audience} \\\"quoted\\\" \\$name\\n\"\n$message = \"line1\nline2\"\n$escaped = \"${name} \\${literal}\"\n")

	got := formatSourceForTest(t, "strings.mcl", src)

	if want, gotStrings := collectStrings(src), collectStrings(got); !slices.Equal(want, gotStrings) {
		t.Fatalf("string raws changed\nwant: %v\ngot:  %v", want, gotStrings)
	}
}

func TestEmptyBlocksStayInlineByDefault(t *testing.T) {
	src := []byte("print \"name\" {\n}\nfile \"/tmp/x\" {\n}\n")

	got := formatSourceForTest(t, "empty-blocks.mcl", src)

	const want = "print \"name\" {}\nfile \"/tmp/x\" {}\n"
	if string(got) != want {
		t.Fatalf("unexpected formatted output\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestEdgeStatementsKeepSpaceAfterArrow(t *testing.T) {
	src := []byte("Exec[\"exec0\"].output->File[\"/tmp/x\"].content\n")

	got := formatSourceForTest(t, "edge-spacing.mcl", src)

	const want = "Exec[\"exec0\"].output -> File[\"/tmp/x\"].content\n"
	if string(got) != want {
		t.Fatalf("unexpected formatted output\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestFunctionExpressionCallBodyStaysMultiline(t *testing.T) {
	src := []byte("$fn = func($x) { printf($x) }\n")

	got := formatSourceForTest(t, "func-body.mcl", src)

	const want = "$fn = func($x) {\n\tprintf($x)\n}\n"
	if string(got) != want {
		t.Fatalf("unexpected formatted output\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestSimpleIfExpressionBranchesInlineByDefault(t *testing.T) {
	src := []byte("$value = if $ready {\n\t\"yes\"\n} else {\n\t\"no\"\n}\n")

	got := formatSourceForTest(t, "if-simple-inline.mcl", src)

	const want = "$value = if $ready { \"yes\" } else { \"no\" }\n"
	if string(got) != want {
		t.Fatalf("unexpected formatted output\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestNestedIfExpressionsApplyBranchInliningRecursively(t *testing.T) {
	src := []byte("$count = if $input > 8 {\n\t8\n} else {\n\tif $input < 1 {\n\t\t1\n\t} else {\n\t\t$input\n\t}\n}\n")

	got := formatSourceForTest(t, "if-nested-recursive.mcl", src)

	const want = "$count = if $input > 8 {\n\t8\n} else {\n\tif $input < 1 { 1 } else { $input }\n}\n"
	if string(got) != want {
		t.Fatalf("unexpected formatted output\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestSmallCollectionsStayInlineByDefault(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "list",
			src:  "$values = [1, 2, 3,]\n",
			want: "$values = [1, 2, 3,]\n",
		},
		{
			name: "map",
			src:  "$values = {\"a\" => 1, \"b\" => 2,}\n",
			want: "$values = {\"a\" => 1, \"b\" => 2,}\n",
		},
		{
			name: "struct",
			src:  "$st = struct{f1 => 42, f2 => true,}\n",
			want: "$st = struct{f1 => 42, f2 => true,}\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSourceForTest(t, tc.name+".mcl", []byte(tc.src))
			if string(got) != tc.want {
				t.Fatalf("unexpected formatted output\nwant:\n%s\ngot:\n%s", tc.want, got)
			}
		})
	}
}

func TestCollectionsKeepExistingInlineLayout(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "list-many",
			src:  "$values = [1, 2, 3, 4,]\n",
			want: "$values = [1, 2, 3, 4,]\n",
		},
		{
			name: "map-many",
			src:  "$values = {\"a\" => 1, \"b\" => 2, \"c\" => 3, \"d\" => 4,}\n",
			want: "$values = {\"a\" => 1, \"b\" => 2, \"c\" => 3, \"d\" => 4,}\n",
		},
		{
			name: "struct-many",
			src:  "$st = struct{f1 => 1, f2 => 2, f3 => 3, f4 => 4,}\n",
			want: "$st = struct{f1 => 1, f2 => 2, f3 => 3, f4 => 4,}\n",
		},
		{
			name: "list-complex",
			src:  "$values = [printf(\"x\"), 2,]\n",
			want: "$values = [printf(\"x\"), 2,]\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSourceForTest(t, tc.name+".mcl", []byte(tc.src))
			if string(got) != tc.want {
				t.Fatalf("unexpected formatted output\nwant:\n%s\ngot:\n%s", tc.want, got)
			}
		})
	}
}

func TestShortMultilineListStaysMultilineByDefault(t *testing.T) {
	src := []byte("$values = [\n\t1,\n\t2,\n]\n")

	got := formatSourceForTest(t, "list-preserve-layout.mcl", src)

	const want = "$values = [\n\t1,\n\t2,\n]\n"
	if string(got) != want {
		t.Fatalf("unexpected formatted output\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestMultilineListOpenerCommentMovesInsideListBody(t *testing.T) {
	src := []byte("$values = [ # defaults\n\t1,\n\t2,\n]\n")

	got := formatSourceForTest(t, "list-opener-comment.mcl", src)

	const want = "$values = [\n\t# defaults\n\t1,\n\t2,\n]\n"
	if string(got) != want {
		t.Fatalf("unexpected formatted output\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestSyntaxErrorsReturnDiagnostics(t *testing.T) {
	doc := syntax.Analyze("bad.mcl", []byte("file \"/tmp/x\" {\n\tcontent => \"hello\"\n}\n"))

	if doc.Root != nil {
		t.Fatalf("expected parse root to be absent on syntax error")
	}
	if len(doc.Diagnostics) == 0 {
		t.Fatalf("expected diagnostics for malformed input")
	}
	if !strings.Contains(doc.Diagnostics[0].Message, "expected \",\"") {
		t.Fatalf("unexpected diagnostic message: %q", doc.Diagnostics[0].Message)
	}
}

func formatSourceForTest(t *testing.T, name string, src []byte) []byte {
	t.Helper()

	doc := syntax.Analyze(name, src)
	if len(doc.Diagnostics) > 0 {
		t.Fatalf("unexpected diagnostics: %+v", doc.Diagnostics)
	}
	got, err := Document(doc)
	if err != nil {
		t.Fatalf("format failed: %+v", err)
	}
	return got
}

func collectComments(src []byte) []string {
	doc := syntax.Analyze("comments.mcl", src)
	out := []string{}
	for _, tok := range doc.Tokens {
		if tok.Kind == langtoken.KindComment {
			out = append(out, tok.Raw)
		}
	}
	return out
}

func collectStrings(src []byte) []string {
	doc := syntax.Analyze("strings.mcl", src)
	out := []string{}
	for _, tok := range doc.Tokens {
		if tok.Kind == langtoken.KindString {
			out = append(out, tok.Raw)
		}
	}
	return out
}
