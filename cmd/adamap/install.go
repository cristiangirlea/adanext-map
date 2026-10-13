package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// mcpJSONName is Claude Code's project MCP file, at the repository root.
const mcpJSONName = ".mcp.json"

// maxMCPJSONBytes is the largest .mcp.json install reads; a real one is a few hundred bytes.
const maxMCPJSONBytes = 1 << 20

func installUsage(w io.Writer, fl *flag.FlagSet) {
	fmt.Fprint(w, `usage: adamap install [--print | --claude | --codex] [--repo dir]

Gives an agent host the entry that starts "adamap mcp" for this repository, with the absolute path
of this adamap. --print (the default) prints the entry for each host; --claude writes it into the
project's .mcp.json, keeping the other servers there; --codex prints the block for
~/.codex/config.toml. Nothing is written outside the repository.

`)
	fl.SetOutput(w)
	fl.PrintDefaults()
}

// checkUTF8 refuses a path that is not valid UTF-8: JSON and TOML hold text, so writing it would
// put U+FFFD where its bytes are, and the host would start a server at a path that does not exist.
func checkUTF8(what, p string) error {
	if utf8.ValidString(p) {
		return nil
	}
	return fmt.Errorf("%s, %q, is not valid UTF-8, so it cannot be written to a JSON or TOML file: use a path that is", what, p)
}

// absUTF8 is p made absolute, refused when it is not valid UTF-8 as given or once made absolute (the
// working directory may be the one). It checks first because on Windows Abs turns a byte that is not
// UTF-8 into U+FFFD without a word.
func absUTF8(what, p string) (string, error) {
	if err := checkUTF8(what, p); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return abs, checkUTF8(what, abs)
}

// osExecutable is where this adamap is; a variable so that a test can stand in for it.
var osExecutable = os.Executable

// serverEntry is the command a host runs to start the server.
type serverEntry struct {
	command string
	args    []string
}

// runInstall is `adamap install`: exit 0 on success, 1 when it fails, 2 for a usage error.
func runInstall(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("install", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	printIt := fl.Bool("print", false, "print the entry for each host (the default)")
	claude := fl.Bool("claude", false, "write the adamap entry into the repository's .mcp.json, Claude Code's project MCP file")
	codex := fl.Bool("codex", false, "print the block to add to ~/.codex/config.toml")
	repo := fl.String("repo", ".", "the repository root: what the server answers about, and where .mcp.json goes")
	usageError := func(err error) int {
		fmt.Fprintf(stderr, "adamap install: %v\n\n", err)
		installUsage(stderr, fl)
		return 2
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			installUsage(stdout, fl)
			return 0
		}
		return usageError(err)
	}
	modes := 0
	for _, on := range []bool{*printIt, *claude, *codex} {
		if on {
			modes++
		}
	}
	switch {
	case fl.NArg() > 0:
		return usageError(fmt.Errorf("unexpected argument %q", fl.Arg(0)))
	case modes > 1:
		return usageError(errors.New("pass one of --print, --claude and --codex"))
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "adamap install: %v\n", err)
		return 1
	}
	root, err := absUTF8("--repo", *repo)
	if err != nil {
		return fail(err)
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return fail(fmt.Errorf("--repo %s is not a directory", root))
	}
	exe, err := osExecutable()
	if err != nil {
		return fail(fmt.Errorf("cannot tell where this adamap is: %w", err))
	}
	if exe, err = absUTF8("the path of this adamap", exe); err != nil {
		return fail(err)
	}
	e := serverEntry{command: exe, args: []string{"mcp", "--repo", root}}

	switch {
	case *claude:
		path := filepath.Join(root, mcpJSONName)
		if err := writeMCPJSON(path, e); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "wrote the adamap server to %s; Claude Code asks to approve it on its next start in this project\n", path)
	case *codex:
		fmt.Fprint(stdout, codexTOML(e))
	default:
		doc, err := setServer(nil, "adamap", e)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Claude Code reads %s at the project root (adamap install --claude writes this there),\nand hrn run --mcp takes the same file:\n\n%s\n", mcpJSONName, doc)
		fmt.Fprintf(stdout, "Codex reads ~/.codex/config.toml; add this block (adamap install --codex prints it alone):\n\n%s", codexTOML(e))
	}
	return 0
}

// writeMCPJSON sets the adamap entry in the .mcp.json at path, creating the file if there is none.
// Everything else in the file stays, in its order. A path that is a link, or not a regular file, is
// refused, so the write cannot leave the repository.
func writeMCPJSON(path string, e serverEntry) error {
	var data []byte
	mode := fs.FileMode(0o644)
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	case !fi.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file (a link?): adamap writes only a plain file inside the repository", path)
	default:
		if data, mode, err = readMCPJSON(path); err != nil {
			return err
		}
	}
	out, err := setServer(data, "adamap", e)
	if err != nil {
		return fmt.Errorf("%s: %w; fix it or move it away, and run install again", path, err)
	}
	if bytes.Equal(out, data) {
		return nil
	}
	return writeFileAtomic(path, out, mode)
}

// readMCPJSON reads the .mcp.json at path and returns it with its permissions. It opens the file
// once and checks what it opened is a regular file (os.Open follows a link swapped in after the
// caller's Lstat, so only a link to something other than a regular file is refused here; the write
// replaces the path itself, never a link's target), and it reads no more than maxMCPJSONBytes
// whatever the file grows to. The file is closed on return, so
// the rename that replaces it is not blocked.
func readMCPJSON(path string) ([]byte, fs.FileMode, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !fi.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%s is not a regular file (a link?): adamap writes only a plain file inside the repository", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxMCPJSONBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if len(data) > maxMCPJSONBytes {
		return nil, 0, fmt.Errorf("%s is larger than %d bytes; edit it by hand", path, maxMCPJSONBytes)
	}
	return data, fi.Mode().Perm(), nil
}

// writeFileAtomic writes data to a temporary file beside path and renames it into place.
func writeFileAtomic(path string, data []byte, mode fs.FileMode) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(f.Name())
		}
	}()
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chmod(f.Name(), mode); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// member is one key of a JSON object and its value as written.
type member struct {
	key   string
	value json.RawMessage
}

// members reads data as one JSON object, keeping its keys in order.
func members(data []byte) ([]member, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		if err != nil {
			return nil, fmt.Errorf("not JSON: %w", err)
		}
		return nil, errors.New("not a JSON object")
	}
	var ms []member
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("not JSON: %w", err)
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("not JSON: %w", err)
		}
		ms = append(ms, member{key, v})
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("not JSON: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("more JSON after the object")
	}
	return ms, nil
}

// set gives key the value v: in place of its first occurrence, which keeps its position, with any
// later duplicate dropped (a JSON reader would take the last); or at the end when ms lacks it.
func set(ms []member, key string, v json.RawMessage) []member {
	out, found := ms[:0:0], false
	for _, m := range ms {
		if m.key != key {
			out = append(out, m)
		} else if !found {
			out, found = append(out, member{key, v}), true
		}
	}
	if !found {
		out = append(out, member{key, v})
	}
	return out
}

// object writes ms as a compact JSON object.
func object(ms []member) (json.RawMessage, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range ms {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := marshal(m.key)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		if err := json.Compact(&b, m.value); err != nil {
			return nil, err
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshal is v as JSON, without escaping <, > and &, which a config file has no reason to.
func marshal(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// setServer is the .mcp.json document data with the server name set to e: its type, command and
// args replaced, its other fields and every other key kept in order. The result is indented by two
// spaces, as Claude Code writes the file. Empty data is an empty document. Of a key written twice,
// the last is read, as a JSON reader takes it, and kept at the first one's place.
func setServer(data []byte, name string, e serverEntry) ([]byte, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var top []member
	if len(bytes.TrimSpace(data)) > 0 {
		var err error
		if top, err = members(data); err != nil {
			return nil, err
		}
	}
	var servers, fields []member
	for _, m := range top {
		if m.key == "mcpServers" {
			var err error
			if servers, err = members(m.value); err != nil {
				return nil, fmt.Errorf("mcpServers: %w", err)
			}
		}
	}
	for _, m := range servers {
		if m.key == name {
			var err error
			if fields, err = members(m.value); err != nil {
				return nil, fmt.Errorf("mcpServers.%s: %w", name, err)
			}
		}
	}
	command, err := marshal(e.command)
	if err != nil {
		return nil, err
	}
	args, err := marshal(e.args)
	if err != nil {
		return nil, err
	}
	fields = set(set(set(fields, "type", json.RawMessage(`"stdio"`)), "command", command), "args", args)
	entry, err := object(fields)
	if err != nil {
		return nil, err
	}
	all, err := object(set(servers, name, entry))
	if err != nil {
		return nil, err
	}
	doc, err := object(set(top, "mcpServers", all))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, doc, "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// codexTOML is the block that adds the server to Codex's ~/.codex/config.toml.
func codexTOML(e serverEntry) string {
	args := make([]string, len(e.args))
	for i, a := range e.args {
		args[i] = tomlString(a)
	}
	return fmt.Sprintf("[mcp_servers.adamap]\ncommand = %s\nargs = [%s]\n", tomlString(e.command), strings.Join(args, ", "))
}

// tomlString is s as a TOML basic string: backslash and quote escaped, control characters written
// as escapes, since TOML allows none of them raw but tab.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
