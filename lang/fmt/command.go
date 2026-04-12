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

// Package langfmt implements the mgmt fmt tooling command.
package langfmt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/purpleidea/mgmt/lang/frontend/diag"
	frontendformat "github.com/purpleidea/mgmt/lang/frontend/format"
	"github.com/purpleidea/mgmt/lang/frontend/syntax"
	"gopkg.in/yaml.v2"
)

const Usage = "Usage: mgmt fmt [-w|--write] [--check] [-c|--config path] [file...]\n\nFormats MCL source using the syntax-preserving frontend.\nWithout a path, input is read from stdin and written to stdout.\nWith -w or --write, one or more files are rewritten in place.\nWith --check, paths that would change are printed and the command exits non-zero.\nWith -c or --config, formatter policy is loaded from a YAML config file.\n"

var errCheckFailed = errors.New("fmt check failed")

type runOptions struct {
	writeInPlace bool
	checkOnly    bool
	configPath   string
	paths        []string
}

// Command runs the formatter over stdin/stdout or files.
type Command struct {
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	ReadFile  func(string) ([]byte, error)
	WriteFile func(string, []byte) error
}

// Run executes the formatter command.
func (obj *Command) Run(ctx context.Context, args []string) error {
	_ = ctx

	options, err := obj.parseArgs(args)
	if err != nil {
		return err
	}

	cfg, err := obj.loadFormatConfig(options.configPath)
	if err != nil {
		return err
	}

	if options.writeInPlace && options.checkOnly {
		return fmt.Errorf("fmt --write and --check cannot be used together")
	}

	if options.writeInPlace {
		if len(options.paths) == 0 {
			return fmt.Errorf("fmt --write requires at least one path")
		}
		for _, path := range options.paths {
			if err := obj.rewriteFile(path, cfg); err != nil {
				return err
			}
		}
		return nil
	}

	if options.checkOnly {
		return obj.runCheck(options.paths, cfg)
	}

	if len(options.paths) == 0 {
		src, err := io.ReadAll(obj.stdin())
		if err != nil {
			return err
		}
		return obj.writeFormattedOutput("<stdin>", src, cfg)
	}

	if len(options.paths) > 1 {
		return fmt.Errorf("multiple files require -w or --check")
	}

	src, err := obj.readFile(options.paths[0])
	if err != nil {
		return err
	}
	return obj.writeFormattedOutput(options.paths[0], src, cfg)
}

func (obj *Command) parseArgs(args []string) (runOptions, error) {
	options := runOptions{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-h", "--help":
			_, err := io.WriteString(obj.stdout(), Usage)
			return runOptions{}, err
		case "-w", "--write":
			options.writeInPlace = true
		case "-check", "--check":
			options.checkOnly = true
		case "-c", "--config":
			if i+1 >= len(args) {
				return runOptions{}, fmt.Errorf("fmt --config requires a path")
			}
			i++
			options.configPath = args[i]
		default:
			if len(arg) > 0 && arg[0] == '-' {
				return runOptions{}, fmt.Errorf("unknown fmt argument: %s", arg)
			}
			options.paths = append(options.paths, arg)
		}
	}
	return options, nil
}

func (obj *Command) runCheck(paths []string, cfg frontendformat.Config) error {
	changed := false
	if len(paths) == 0 {
		src, err := io.ReadAll(obj.stdin())
		if err != nil {
			return err
		}
		differs, err := obj.checkFormatted("<stdin>", src, cfg)
		if err != nil {
			return err
		}
		if differs {
			return errCheckFailed
		}
		return nil
	}

	for _, path := range paths {
		src, err := obj.readFile(path)
		if err != nil {
			return err
		}
		differs, err := obj.checkFormatted(path, src, cfg)
		if err != nil {
			return err
		}
		changed = changed || differs
	}
	if changed {
		return errCheckFailed
	}
	return nil
}

func (obj *Command) rewriteFile(path string, cfg frontendformat.Config) error {
	src, err := obj.readFile(path)
	if err != nil {
		return err
	}
	formatted, err := obj.formatSource(path, src, cfg)
	if err != nil {
		return err
	}
	if bytes.Equal(src, formatted) {
		return nil
	}
	return obj.writeFile(path, formatted)
}

func (obj *Command) loadFormatConfig(path string) (frontendformat.Config, error) {
	cfg := frontendformat.DefaultConfig()
	if path == "" {
		return cfg, nil
	}

	data, err := obj.readFile(path)
	if err != nil {
		return frontendformat.Config{}, err
	}
	var overlay frontendformat.ConfigOverlay
	if err := yaml.UnmarshalStrict(data, &overlay); err != nil {
		return frontendformat.Config{}, fmt.Errorf("invalid fmt config %s: %w", path, err)
	}
	overlay.ApplyTo(&cfg)
	return cfg, nil
}

func (obj *Command) formatSource(name string, src []byte, cfg frontendformat.Config) ([]byte, error) {
	doc := syntax.Analyze(name, src)
	if len(doc.Diagnostics) > 0 {
		if err := writeDiagnostics(obj.stderr(), doc.Diagnostics); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("formatting failed for %s", name)
	}
	return frontendformat.DocumentWithConfig(doc, cfg)
}

func (obj *Command) writeFormattedOutput(name string, src []byte, cfg frontendformat.Config) error {
	formatted, err := obj.formatSource(name, src, cfg)
	if err != nil {
		return err
	}
	_, err = obj.stdout().Write(formatted)
	return err
}

func (obj *Command) checkFormatted(name string, src []byte, cfg frontendformat.Config) (bool, error) {
	formatted, err := obj.formatSource(name, src, cfg)
	if err != nil {
		return false, err
	}
	if bytes.Equal(src, formatted) {
		return false, nil
	}
	_, err = fmt.Fprintln(obj.stdout(), name)
	return true, err
}

func writeDiagnostics(w io.Writer, items []diag.Diagnostic) error {
	for _, item := range items {
		pos := item.Span.StartPosition()
		name := item.Span.File.Name()
		if name == "" {
			name = "<stdin>"
		}
		if _, err := fmt.Fprintf(w, "%s:%d:%d: %s: %s\n", name, pos.Line+1, pos.Column+1, item.Severity, item.Message); err != nil {
			return err
		}
		for _, related := range item.Related {
			relatedPos := related.Span.StartPosition()
			relatedName := related.Span.File.Name()
			if relatedName == "" {
				relatedName = name
			}
			if _, err := fmt.Fprintf(w, "%s:%d:%d: note: %s\n", relatedName, relatedPos.Line+1, relatedPos.Column+1, related.Message); err != nil {
				return err
			}
		}
	}
	return nil
}

func (obj *Command) stdin() io.Reader {
	if obj != nil && obj.Stdin != nil {
		return obj.Stdin
	}
	return os.Stdin
}

func (obj *Command) stdout() io.Writer {
	if obj != nil && obj.Stdout != nil {
		return obj.Stdout
	}
	return os.Stdout
}

func (obj *Command) stderr() io.Writer {
	if obj != nil && obj.Stderr != nil {
		return obj.Stderr
	}
	return os.Stderr
}

func (obj *Command) readFile(path string) ([]byte, error) {
	if obj != nil && obj.ReadFile != nil {
		return obj.ReadFile(path)
	}
	return os.ReadFile(path)
}

func (obj *Command) writeFile(path string, src []byte) error {
	if obj != nil && obj.WriteFile != nil {
		return obj.WriteFile(path, src)
	}
	return os.WriteFile(path, src, 0o644)
}
