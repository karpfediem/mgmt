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

// Package lsp provides a small syntax-only LSP server over stdio.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/purpleidea/mgmt/lang/frontend/diag"
	"github.com/purpleidea/mgmt/lang/frontend/source"
	"github.com/purpleidea/mgmt/lang/frontend/syntax"
)

const (
	jsonrpcVersion             = "2.0"
	textDocumentSyncFull       = 1
	lspErrorCodeParseError     = -32700
	lspErrorCodeInvalidParams  = -32602
	lspErrorCodeMethodNotFound = -32601
)

// Server is a minimal syntax-only LSP server.
type Server struct {
	docs map[string]*syntax.Document
}

// NewServer returns a new syntax-only LSP server.
func NewServer() *Server {
	return &Server{
		docs: make(map[string]*syntax.Document),
	}
}

// ServeStdio runs the server on the supplied stdio-like streams.
func ServeStdio(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	return NewServer().Serve(ctx, stdin, stdout)
}

// Serve processes JSON-RPC 2.0 LSP messages until exit or EOF.
func (obj *Server) Serve(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	reader := bufio.NewReader(stdin)
	writer := bufio.NewWriter(stdout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		body, err := readMessage(reader)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			if writeErr := writeError(writer, nil, lspErrorCodeParseError, err.Error()); writeErr != nil {
				return writeErr
			}
			continue
		}

		stop, err := obj.handle(writer, body)
		if err != nil {
			return err
		}
		if stop {
			return writer.Flush()
		}
	}
}

type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
	ServerInfo   serverInfo         `json:"serverInfo"`
}

type serverCapabilities struct {
	TextDocumentSync       int  `json:"textDocumentSync"`
	DocumentSymbolProvider bool `json:"documentSymbolProvider"`
}

type serverInfo struct {
	Name string `json:"name"`
}

type textDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type versionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type textDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

type didChangeParams struct {
	TextDocument   versionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []textDocumentContentChangeEvent `json:"contentChanges"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type documentSymbolParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type publishDiagnosticsParams struct {
	URI         string          `json:"uri"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
}

type lspDiagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"`
	Message  string   `json:"message"`
}

type lspDocumentSymbol struct {
	Name           string              `json:"name"`
	Kind           int                 `json:"kind"`
	Range          lspRange            `json:"range"`
	SelectionRange lspRange            `json:"selectionRange"`
	Children       []lspDocumentSymbol `json:"children,omitempty"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

func (obj *Server) handle(writer *bufio.Writer, body []byte) (bool, error) {
	msg := wireMessage{}
	if err := json.Unmarshal(body, &msg); err != nil {
		return false, writeError(writer, nil, lspErrorCodeParseError, err.Error())
	}
	hasID := len(msg.ID) > 0
	if msg.JSONRPC != "" && msg.JSONRPC != jsonrpcVersion {
		if !hasID {
			return false, nil
		}
		return false, writeError(writer, msg.ID, lspErrorCodeParseError, "unsupported jsonrpc version")
	}
	if msg.Method == "" {
		return false, nil
	}

	switch msg.Method {
	case "initialize":
		result := initializeResult{
			Capabilities: serverCapabilities{
				TextDocumentSync:       textDocumentSyncFull,
				DocumentSymbolProvider: true,
			},
			ServerInfo: serverInfo{Name: "mgmt-lsp"},
		}
		return false, writeResult(writer, msg.ID, result)
	case "initialized":
		return false, nil
	case "shutdown":
		return false, writeResult(writer, msg.ID, nil)
	case "exit":
		return true, nil
	case "textDocument/didOpen":
		params := didOpenParams{}
		if err := decodeParams(msg.Params, &params); err != nil {
			if !hasID {
				return false, nil
			}
			return false, writeError(writer, msg.ID, lspErrorCodeInvalidParams, err.Error())
		}
		obj.docs[params.TextDocument.URI] = syntax.Analyze(params.TextDocument.URI, []byte(params.TextDocument.Text))
		return false, obj.publishDiagnostics(writer, params.TextDocument.URI)
	case "textDocument/didChange":
		params := didChangeParams{}
		if err := decodeParams(msg.Params, &params); err != nil {
			if !hasID {
				return false, nil
			}
			return false, writeError(writer, msg.ID, lspErrorCodeInvalidParams, err.Error())
		}
		if len(params.ContentChanges) == 0 {
			if !hasID {
				return false, nil
			}
			return false, writeError(writer, msg.ID, lspErrorCodeInvalidParams, "expected at least one content change")
		}
		text := params.ContentChanges[len(params.ContentChanges)-1].Text
		obj.docs[params.TextDocument.URI] = syntax.Analyze(params.TextDocument.URI, []byte(text))
		return false, obj.publishDiagnostics(writer, params.TextDocument.URI)
	case "textDocument/didClose":
		params := didCloseParams{}
		if err := decodeParams(msg.Params, &params); err != nil {
			if !hasID {
				return false, nil
			}
			return false, writeError(writer, msg.ID, lspErrorCodeInvalidParams, err.Error())
		}
		delete(obj.docs, params.TextDocument.URI)
		return false, writeNotification(writer, "textDocument/publishDiagnostics", publishDiagnosticsParams{
			URI:         params.TextDocument.URI,
			Diagnostics: []lspDiagnostic{},
		})
	case "textDocument/documentSymbol":
		params := documentSymbolParams{}
		if err := decodeParams(msg.Params, &params); err != nil {
			if !hasID {
				return false, nil
			}
			return false, writeError(writer, msg.ID, lspErrorCodeInvalidParams, err.Error())
		}
		doc := obj.docs[params.TextDocument.URI]
		if doc == nil {
			return false, writeResult(writer, msg.ID, []lspDocumentSymbol{})
		}
		return false, writeResult(writer, msg.ID, mapSymbols(doc.Symbols))
	default:
		if len(msg.ID) == 0 {
			return false, nil
		}
		return false, writeError(writer, msg.ID, lspErrorCodeMethodNotFound, fmt.Sprintf("method not found: %s", msg.Method))
	}
}

func (obj *Server) publishDiagnostics(writer *bufio.Writer, uri string) error {
	doc := obj.docs[uri]
	if doc == nil {
		return writeNotification(writer, "textDocument/publishDiagnostics", publishDiagnosticsParams{
			URI:         uri,
			Diagnostics: []lspDiagnostic{},
		})
	}
	return writeNotification(writer, "textDocument/publishDiagnostics", publishDiagnosticsParams{
		URI:         uri,
		Diagnostics: mapDiagnostics(doc.Diagnostics),
	})
}

func decodeParams(raw json.RawMessage, out interface{}) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func mapDiagnostics(items []diag.Diagnostic) []lspDiagnostic {
	out := make([]lspDiagnostic, 0, len(items))
	for _, item := range items {
		out = append(out, lspDiagnostic{
			Range:    mapSpan(item.Span),
			Severity: mapSeverity(item.Severity),
			Message:  item.Message,
		})
	}
	return out
}

func mapSymbols(items []syntax.Symbol) []lspDocumentSymbol {
	out := make([]lspDocumentSymbol, 0, len(items))
	for _, item := range items {
		out = append(out, lspDocumentSymbol{
			Name:           item.Name,
			Kind:           mapSymbolKind(item.Kind),
			Range:          mapSpan(item.Span),
			SelectionRange: mapSpan(item.SelectionSpan),
			Children:       mapSymbols(item.Children),
		})
	}
	return out
}

func mapSeverity(severity diag.Severity) int {
	switch severity {
	case diag.SeverityWarning:
		return 2
	case diag.SeverityNote:
		return 3
	default:
		return 1
	}
}

func mapSymbolKind(kind syntax.SymbolKind) int {
	switch kind {
	case syntax.SymbolKindImport:
		return 3
	case syntax.SymbolKindInclude:
		return 2
	case syntax.SymbolKindFunction:
		return 12
	case syntax.SymbolKindVariable:
		return 13
	case syntax.SymbolKindClass:
		return 5
	default:
		return 19
	}
}

func mapSpan(span source.Span) lspRange {
	return lspRange{
		Start: mapPosition(span.File, span.Start),
		End:   mapPosition(span.File, span.End),
	}
}

func mapPosition(file *source.File, offset int) lspPosition {
	if file == nil {
		return lspPosition{}
	}

	pos := file.Position(offset)
	lineStart := file.Offset(pos.Line, 0)
	char := utf16Length(file.Bytes()[lineStart:offset])
	return lspPosition{
		Line:      pos.Line,
		Character: char,
	}
}

func utf16Length(buf []byte) int {
	runes := []rune(string(buf))
	return len(utf16.Encode(runes))
}

func readMessage(reader *bufio.Reader) ([]byte, error) {
	length := -1

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF && length == -1 && line == "" {
				return nil, io.EOF
			}
			return nil, err
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid header: %q", line)
		}

		name := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		if name != "content-length" {
			continue
		}

		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid content length: %q", value)
		}
		length = n
	}

	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

func writeNotification(writer *bufio.Writer, method string, params interface{}) error {
	msg := map[string]interface{}{
		"jsonrpc": jsonrpcVersion,
		"method":  method,
		"params":  params,
	}
	return writeMessage(writer, msg)
}

func writeResult(writer *bufio.Writer, id json.RawMessage, result interface{}) error {
	return writeMessage(writer, map[string]interface{}{
		"jsonrpc": jsonrpcVersion,
		"id":      responseID(id),
		"result":  result,
	})
}

func writeError(writer *bufio.Writer, id json.RawMessage, code int, message string) error {
	return writeMessage(writer, map[string]interface{}{
		"jsonrpc": jsonrpcVersion,
		"id":      responseID(id),
		"error": &responseError{
			Code:    code,
			Message: message,
		},
	})
}

func responseID(id json.RawMessage) interface{} {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func writeMessage(writer *bufio.Writer, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	if _, err := writer.Write(body); err != nil {
		return err
	}
	return writer.Flush()
}
