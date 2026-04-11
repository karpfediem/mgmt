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

package syntax

import "testing"

func TestAnalyzeRoundTrip(t *testing.T) {
	source := "# comment\n$answer = 42\nnoop \"n1\" {}\n"
	doc := Analyze("roundtrip.mcl", []byte(source))

	if got := doc.RawText(); got != source {
		t.Fatalf("round-trip mismatch\nwant: %q\ngot:  %q", source, got)
	}
}

func TestAnalyzeMultilineStringStaysDiagnosticFree(t *testing.T) {
	source := "$msg = \"hello\nworld\"\n"
	doc := Analyze("multiline.mcl", []byte(source))

	if got := doc.RawText(); got != source {
		t.Fatalf("round-trip mismatch\nwant: %q\ngot:  %q", source, got)
	}
	if len(doc.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics for multiline string: %+v", doc.Diagnostics)
	}
}

func TestAnalyzeLexerErrorsBecomeDiagnostics(t *testing.T) {
	doc := Analyze("bad.mcl", []byte("@\n"))

	if len(doc.Diagnostics) != 1 {
		t.Fatalf("unexpected diagnostic count: got %d, want 1", len(doc.Diagnostics))
	}
	if got, want := doc.Diagnostics[0].Message, "unexpected character \"@\""; got != want {
		t.Fatalf("unexpected diagnostic message: got %q, want %q", got, want)
	}
}

func TestAnalyzeCarriageReturnBecomesDiagnostic(t *testing.T) {
	doc := Analyze("bad-crlf.mcl", []byte("noop \"n1\" {}\r\n"))

	if len(doc.Diagnostics) != 1 {
		t.Fatalf("unexpected diagnostic count: got %d, want 1", len(doc.Diagnostics))
	}
	if got, want := doc.Diagnostics[0].Message, "unrecognized carriage return"; got != want {
		t.Fatalf("unexpected diagnostic message: got %q, want %q", got, want)
	}
}

func TestAnalyzeDelimiterDiagnostics(t *testing.T) {
	doc := Analyze("bad-block.mcl", []byte("noop \"n1\" {\n"))

	if len(doc.Diagnostics) != 1 {
		t.Fatalf("unexpected diagnostic count: got %d, want 1", len(doc.Diagnostics))
	}
	if got, want := doc.Diagnostics[0].Message, "unclosed delimiter \"{\""; got != want {
		t.Fatalf("unexpected diagnostic message: got %q, want %q", got, want)
	}
}

func TestAnalyzeDocumentSymbols(t *testing.T) {
	source := "import \"fmt\" as fm\n$answer = 42\n$load float = 0.5\nfunc greet($name) {\n}\nclass firewall:zone($name) {\n}\ndocker:image \"nginx\" {}\nhttp:server:ui:input $id {}\ncollect file \"/tmp/hello\" {}\ninclude base.repo(\"x\") as repo\ninclude helper.inner\n"
	doc := Analyze("symbols.mcl", []byte(source))

	if len(doc.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", doc.Diagnostics)
	}

	want := []struct {
		name string
		kind SymbolKind
	}{
		{name: "fm", kind: SymbolKindImport},
		{name: "answer", kind: SymbolKindVariable},
		{name: "load", kind: SymbolKindVariable},
		{name: "greet", kind: SymbolKindFunction},
		{name: "firewall:zone", kind: SymbolKindClass},
		{name: "docker:image \"nginx\"", kind: SymbolKindResource},
		{name: "http:server:ui:input $id", kind: SymbolKindResource},
		{name: "file \"/tmp/hello\"", kind: SymbolKindResource},
		{name: "repo", kind: SymbolKindInclude},
		{name: "helper.inner", kind: SymbolKindInclude},
	}

	if len(doc.Symbols) != len(want) {
		t.Fatalf("unexpected symbol count: got %d, want %d", len(doc.Symbols), len(want))
	}
	for i, exp := range want {
		if got := doc.Symbols[i]; got.Name != exp.name || got.Kind != exp.kind {
			t.Fatalf("symbol %d mismatch: got (%q, %s), want (%q, %s)", i, got.Name, got.Kind, exp.name, exp.kind)
		}
	}
}
