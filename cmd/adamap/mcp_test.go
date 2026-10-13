package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

// runMainEnv, when set in the environment, makes the test binary run adamap's main instead of the
// tests, so a test can start adamap as a process of its own.
const runMainEnv = "ADAMAP_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		main()
	}
	os.Exit(m.Run())
}

// startAdamap starts adamap with args as a process of its own, with pipes to its stdin and stdout.
func startAdamap(t *testing.T, args ...string) (cmd *exec.Cmd, stdin io.WriteCloser, stdout *bufio.Reader, stderr *bytes.Buffer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	cmd = exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(), runMainEnv+"=1")
	stderr = &bytes.Buffer{}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd, stdin, bufio.NewReader(out), stderr
}

// send writes one JSON-RPC message.
func send(t *testing.T, w io.Writer, msg string) {
	t.Helper()
	if _, err := io.WriteString(w, msg+"\n"); err != nil {
		t.Fatal(err)
	}
}

// receive reads one line of stdout and decodes it as a JSON-RPC message.
func receive(t *testing.T, r *bufio.Reader) map[string]any {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read stdout: %v (got %q)", err, line)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil || m["jsonrpc"] != "2.0" {
		t.Fatalf("stdout line is not a JSON-RPC message: %q", line)
	}
	return m
}

func TestMCPWritesOnlyProtocolToStdout(t *testing.T) {
	repo := queryRepo(t)
	cmd, stdin, stdout, stderr := startAdamap(t, "mcp", "--repo", repo)

	send(t, stdin, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`)
	init := receive(t, stdout)
	result, _ := init["result"].(map[string]any)
	info, _ := result["serverInfo"].(map[string]any)
	if init["id"] != float64(1) || info["name"] != "adamap" || info["version"] != Version {
		t.Fatalf("initialize answer %v", init)
	}
	send(t, stdin, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(t, stdin, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"outline","arguments":{"file":"calls/a.go"}}}`)
	answer := receive(t, stdout)
	result, _ = answer["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if answer["id"] != float64(2) || len(content) != 1 {
		t.Fatalf("tools/call answer %v", answer)
	}
	if text := content[0].(map[string]any)["text"]; text != "calls/a.go:2 Function A\ncalls/a.go:8 Function B\ncalls/a.go:14 Function D\n" {
		t.Errorf("outline over stdio: %q", text)
	}

	// Closing stdin ends the session; whatever else reached stdout must be protocol too.
	stdin.Close()
	rest, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(rest)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil || m["jsonrpc"] != "2.0" {
			t.Errorf("stdout holds something other than protocol: %q", line)
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("adamap mcp should exit 0 when stdin closes: %v; stderr %q", err, stderr)
	}
	if !strings.Contains(stderr.String(), "adamap mcp: serving") {
		t.Errorf("the start goes to stderr: %q", stderr)
	}
}

func TestMCPEndsWhenStdinCloses(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runMCP([]string{"--repo", queryRepo(t)}, strings.NewReader(""), &out, &errOut)
	if code != 0 || out.Len() != 0 {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

func TestMCPWithoutAStoreStillServesAndSaysSoOnStderr(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runMCP([]string{"--repo", t.TempDir()}, strings.NewReader(""), &out, &errOut)
	if code != 0 || out.Len() != 0 || !strings.Contains(errOut.String(), "adamap index") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

func TestMCPUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"mcp", "extra"},
		{"mcp", "--commit", "abc"},
		{"mcp", "--nope"},
	} {
		code, out, errOut := runCmd(args...)
		if code != 2 || out != "" || !strings.Contains(errOut, "usage: adamap mcp") {
			t.Errorf("%q: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	code, out, _ := runCmd("mcp", "--repo", t.TempDir()+"/missing")
	if code != 1 || out != "" {
		t.Errorf("a --repo that is not a directory: exit %d, stdout %q", code, out)
	}
	code, out, _ = runCmd("mcp", "-h")
	if code != 0 || !strings.Contains(out, "--commit") {
		t.Errorf("mcp -h: exit %d, stdout %q", code, out)
	}
}

func TestMCPAnswersFromAPinnedCommit(t *testing.T) {
	repo := queryRepo(t)
	indexInto(t, repo, otherCommit, sciptest.Index())
	cmd, stdin, stdout, _ := startAdamap(t, "mcp", "--repo", repo, "--commit", testCommit)
	send(t, stdin, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`)
	receive(t, stdout)
	send(t, stdin, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(t, stdin, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"outline","arguments":{"file":"calls/a.go"}}}`)
	answer := receive(t, stdout)
	if result, _ := answer["result"].(map[string]any); result["isError"] == true {
		t.Errorf("a pinned commit should answer from its own store: %v", answer)
	}
	stdin.Close()
	io.Copy(io.Discard, stdout)
	cmd.Wait()
}

func TestHelpListsMCP(t *testing.T) {
	if out := mustRun(t, "help"); !strings.Contains(out, "\n  mcp ") {
		t.Errorf("help should list mcp: %q", out)
	}
}
