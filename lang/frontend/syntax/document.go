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

import (
	"github.com/purpleidea/mgmt/lang/frontend/diag"
	langsource "github.com/purpleidea/mgmt/lang/frontend/source"
	langtoken "github.com/purpleidea/mgmt/lang/frontend/token"
)

// SymbolKind identifies a syntax-only symbol extracted from a source snapshot.
type SymbolKind string

const (
	SymbolKindVariable SymbolKind = "variable"
	SymbolKindResource SymbolKind = "resource"
	SymbolKindFunction SymbolKind = "function"
	SymbolKindClass    SymbolKind = "class"
	SymbolKindImport   SymbolKind = "import"
	SymbolKindInclude  SymbolKind = "include"
)

// Symbol is a syntax-only document symbol with source provenance.
type Symbol struct {
	Name          string
	Kind          SymbolKind
	Span          langsource.Span
	SelectionSpan langsource.Span
	Children      []Symbol
}

// Document is the syntax-only analysis result for one source snapshot.
type Document struct {
	File        *langsource.File
	Tokens      []langtoken.Token
	Diagnostics []diag.Diagnostic
	Symbols     []Symbol
	Root        *FileNode
}

// Analyze constructs a syntax document from source bytes.
func Analyze(name string, source []byte) *Document {
	return AnalyzeFile(langsource.NewFile(name, source))
}

// AnalyzeFile constructs a syntax document from an existing source snapshot.
func AnalyzeFile(file *langsource.File) *Document {
	if file == nil {
		file = langsource.NewFile("", nil)
	}

	tokens := langtoken.Scan(file)
	diagnostics := collectDiagnostics(tokens)
	var root *FileNode
	if len(diagnostics) == 0 {
		var parseDiagnostics []diag.Diagnostic
		root, parseDiagnostics = parseFileNode(file, tokens)
		diagnostics = append(diagnostics, parseDiagnostics...)
	}
	return &Document{
		File:        file,
		Tokens:      tokens,
		Diagnostics: diagnostics,
		Symbols:     collectSymbols(tokens),
		Root:        root,
	}
}

// RawText reconstructs the original source represented by this document.
func (obj *Document) RawText() string {
	if obj == nil {
		return ""
	}
	return langtoken.JoinRaw(obj.Tokens)
}
