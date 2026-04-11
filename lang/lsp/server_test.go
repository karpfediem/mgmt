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

package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"testing"
)

func TestServerInitializeShutdown(t *testing.T) {
	outputs := runSession(t,
		map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params":  map[string]interface{}{},
		},
		map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "initialized",
			"params":  map[string]interface{}{},
		},
		map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "shutdown",
		},
		map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "exit",
		},
	)

	initResp := responseByID(t, outputs, 1)
	caps := initResp["result"].(map[string]interface{})["capabilities"].(map[string]interface{})
	if got, want := int(caps["textDocumentSync"].(float64)), textDocumentSyncFull; got != want {
		t.Fatalf("unexpected textDocumentSync: got %d, want %d", got, want)
	}
	if got := caps["documentSymbolProvider"].(bool); !got {
		t.Fatalf("expected documentSymbolProvider to be enabled")
	}

	shutdownResp := responseByID(t, outputs, 2)
	if _, exists := shutdownResp["error"]; exists {
		t.Fatalf("unexpected shutdown error: %+v", shutdownResp["error"])
	}
	if result, exists := shutdownResp["result"]; !exists || result != nil {
		t.Fatalf("expected shutdown response to include result:null, got %+v", shutdownResp)
	}
}

func TestServerPublishesDiagnosticsAndTracksChanges(t *testing.T) {
	outputs := runSession(t,
		initializeMessage(),
		map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "textDocument/didOpen",
			"params": map[string]interface{}{
				"textDocument": map[string]interface{}{
					"uri":        "file:///test.mcl",
					"languageId": "mcl",
					"version":    1,
					"text":       "noop \"n1\" {\n",
				},
			},
		},
		map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "textDocument/didChange",
			"params": map[string]interface{}{
				"textDocument": map[string]interface{}{
					"uri":     "file:///test.mcl",
					"version": 2,
				},
				"contentChanges": []map[string]interface{}{
					{"text": "noop \"n1\" {}\n"},
				},
			},
		},
		shutdownMessage(),
		exitMessage(),
	)

	diagnosticNotifications := notificationsByMethod(outputs, "textDocument/publishDiagnostics")
	if len(diagnosticNotifications) != 2 {
		t.Fatalf("unexpected diagnostics notification count: got %d, want 2", len(diagnosticNotifications))
	}

	first := diagnosticNotifications[0]["params"].(map[string]interface{})["diagnostics"].([]interface{})
	if len(first) != 1 {
		t.Fatalf("unexpected diagnostic count after open: got %d, want 1", len(first))
	}

	second := diagnosticNotifications[1]["params"].(map[string]interface{})["diagnostics"].([]interface{})
	if len(second) != 0 {
		t.Fatalf("expected diagnostics to clear after change, got %+v", second)
	}
}

func TestServerDocumentSymbol(t *testing.T) {
	outputs := runSession(t,
		initializeMessage(),
		map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "textDocument/didOpen",
			"params": map[string]interface{}{
				"textDocument": map[string]interface{}{
					"uri":        "file:///symbols.mcl",
					"languageId": "mcl",
					"version":    1,
					"text":       "$answer = 42\nnoop \"n1\" {}\n",
				},
			},
		},
		map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "textDocument/documentSymbol",
			"params": map[string]interface{}{
				"textDocument": map[string]interface{}{
					"uri": "file:///symbols.mcl",
				},
			},
		},
		map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "textDocument/didClose",
			"params": map[string]interface{}{
				"textDocument": map[string]interface{}{
					"uri": "file:///symbols.mcl",
				},
			},
		},
		shutdownMessage(),
		exitMessage(),
	)

	resp := responseByID(t, outputs, 2)
	raw := resp["result"].([]interface{})
	if len(raw) != 2 {
		t.Fatalf("unexpected symbol count: got %d, want 2", len(raw))
	}

	first := raw[0].(map[string]interface{})
	if got, want := first["name"].(string), "answer"; got != want {
		t.Fatalf("unexpected first symbol name: got %q, want %q", got, want)
	}

	closeNotifications := notificationsByMethod(outputs, "textDocument/publishDiagnostics")
	last := closeNotifications[len(closeNotifications)-1]["params"].(map[string]interface{})["diagnostics"].([]interface{})
	if len(last) != 0 {
		t.Fatalf("expected didClose to clear diagnostics, got %+v", last)
	}
}

func TestServerParseErrorUsesNullID(t *testing.T) {
	outputs := runRawSession(t, "Content-Length: 1\r\n\r\n{")
	if len(outputs) != 1 {
		t.Fatalf("unexpected output count: got %d, want 1", len(outputs))
	}
	if id, exists := outputs[0]["id"]; !exists || id != nil {
		t.Fatalf("expected parse error response to include id:null, got %+v", outputs[0])
	}
}

func runSession(t *testing.T, messages ...map[string]interface{}) []map[string]interface{} {
	t.Helper()

	var input bytes.Buffer
	for _, msg := range messages {
		writeClientMessage(t, &input, msg)
	}

	var output bytes.Buffer
	server := NewServer()
	if err := server.Serve(context.Background(), &input, &output); err != nil {
		t.Fatalf("Serve failed: %+v", err)
	}

	return readServerMessages(t, &output)
}

func runRawSession(t *testing.T, input string) []map[string]interface{} {
	t.Helper()

	var output bytes.Buffer
	server := NewServer()
	if err := server.Serve(context.Background(), bytes.NewBufferString(input), &output); err != nil {
		t.Fatalf("Serve failed: %+v", err)
	}

	return readServerMessages(t, &output)
}

func writeClientMessage(t *testing.T, output io.Writer, payload interface{}) {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal failed: %+v", err)
	}
	if _, err := output.Write([]byte("Content-Length: ")); err != nil {
		t.Fatalf("Write failed: %+v", err)
	}
	if _, err := output.Write([]byte(stringifyLen(len(body)))); err != nil {
		t.Fatalf("Write failed: %+v", err)
	}
	if _, err := output.Write([]byte("\r\n\r\n")); err != nil {
		t.Fatalf("Write failed: %+v", err)
	}
	if _, err := output.Write(body); err != nil {
		t.Fatalf("Write failed: %+v", err)
	}
}

func readServerMessages(t *testing.T, input io.Reader) []map[string]interface{} {
	t.Helper()

	reader := bufio.NewReader(input)
	outputs := []map[string]interface{}{}
	for {
		body, err := readMessage(reader)
		if err == io.EOF {
			return outputs
		}
		if err != nil {
			t.Fatalf("readMessage failed: %+v", err)
		}

		msg := map[string]interface{}{}
		if err := json.Unmarshal(body, &msg); err != nil {
			t.Fatalf("Unmarshal failed: %+v", err)
		}
		outputs = append(outputs, msg)
	}
}

func responseByID(t *testing.T, outputs []map[string]interface{}, id float64) map[string]interface{} {
	t.Helper()

	for _, msg := range outputs {
		if got, ok := msg["id"].(float64); ok && got == id {
			return msg
		}
	}
	t.Fatalf("response with id %.0f not found in %+v", id, outputs)
	return nil
}

func notificationsByMethod(outputs []map[string]interface{}, method string) []map[string]interface{} {
	list := []map[string]interface{}{}
	for _, msg := range outputs {
		if got, ok := msg["method"].(string); ok && got == method {
			list = append(list, msg)
		}
	}
	return list
}

func initializeMessage() map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]interface{}{},
	}
}

func shutdownMessage() map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      99,
		"method":  "shutdown",
	}
}

func exitMessage() map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "exit",
	}
}

func stringifyLen(n int) string {
	return strconv.Itoa(n)
}
