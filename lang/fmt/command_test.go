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

package langfmt

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandFormatsStdinToStdout(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd := &Command{
		Stdin:  strings.NewReader("$value=true\n"),
		Stdout: stdout,
		Stderr: stderr,
	}

	if err := cmd.Run(context.Background(), nil); err != nil {
		t.Fatalf("command failed: %+v", err)
	}
	if got, want := stdout.String(), "$value = true\n"; got != want {
		t.Fatalf("unexpected stdout\nwant:\n%s\ngot:\n%s", want, got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestCommandFormatsFileToStdout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "example.mcl")
	if err := os.WriteFile(path, []byte("$value=true\n"), 0o644); err != nil {
		t.Fatalf("failed to seed file: %+v", err)
	}

	stdout := &bytes.Buffer{}
	cmd := &Command{
		Stdout: stdout,
		Stderr: &bytes.Buffer{},
	}

	if err := cmd.Run(context.Background(), []string{path}); err != nil {
		t.Fatalf("command failed: %+v", err)
	}
	if got, want := stdout.String(), "$value = true\n"; got != want {
		t.Fatalf("unexpected stdout\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestCommandWriteInPlace(t *testing.T) {
	for _, flag := range []string{"-w", "--write"} {
		t.Run(flag, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "example.mcl")
			if err := os.WriteFile(path, []byte("$value=true\n"), 0o644); err != nil {
				t.Fatalf("failed to seed file: %+v", err)
			}

			cmd := &Command{
				Stdout: &bytes.Buffer{},
				Stderr: &bytes.Buffer{},
			}
			if err := cmd.Run(context.Background(), []string{flag, path}); err != nil {
				t.Fatalf("command failed: %+v", err)
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("failed to read rewritten file: %+v", err)
			}
			if want := "$value = true\n"; string(got) != want {
				t.Fatalf("unexpected file contents\nwant:\n%s\ngot:\n%s", want, got)
			}
		})
	}
}

func TestCommandCheck(t *testing.T) {
	dir := t.TempDir()
	formatted := filepath.Join(dir, "formatted.mcl")
	unformatted := filepath.Join(dir, "unformatted.mcl")
	if err := os.WriteFile(formatted, []byte("$value = true\n"), 0o644); err != nil {
		t.Fatalf("failed to seed formatted file: %+v", err)
	}
	if err := os.WriteFile(unformatted, []byte("$other=false\n"), 0o644); err != nil {
		t.Fatalf("failed to seed unformatted file: %+v", err)
	}

	stdout := &bytes.Buffer{}
	cmd := &Command{
		Stdout: stdout,
		Stderr: &bytes.Buffer{},
	}

	if err := cmd.Run(context.Background(), []string{"--check", formatted}); err != nil {
		t.Fatalf("command failed for formatted file: %+v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected stdout for formatted file: %s", stdout.String())
	}

	err := cmd.Run(context.Background(), []string{"--check", formatted, unformatted})
	if !errors.Is(err, errCheckFailed) {
		t.Fatalf("unexpected check error: %+v", err)
	}
	if got, want := stdout.String(), unformatted+"\n"; got != want {
		t.Fatalf("unexpected stdout\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestCommandPrintsDiagnosticsOnSyntaxError(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd := &Command{
		Stdin:  strings.NewReader("file \"/tmp/x\" {\n\tcontent => \"hello\"\n}\n"),
		Stdout: stdout,
		Stderr: stderr,
	}

	err := cmd.Run(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected syntax error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected stdout: %s", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "expected \",\"") {
		t.Fatalf("unexpected diagnostics: %s", got)
	}
}

func TestCommandRejectsLegacyCheckFlag(t *testing.T) {
	cmd := &Command{
		Stdin:  strings.NewReader("$value = true\n"),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}

	err := cmd.Run(context.Background(), []string{"-check"})
	if err == nil {
		t.Fatalf("expected flag error")
	}
	if !strings.Contains(err.Error(), "unknown fmt argument: -check") {
		t.Fatalf("unexpected error: %+v", err)
	}
}
