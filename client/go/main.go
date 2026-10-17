// Command crossroads is the prebuilt, memory-safe reference client for the
// Agent Crossroads skill API. It covers the full agent-facing surface from
// the skill contract so onboarding agents do not have to write a harness.
//
// Configuration via environment (flags override):
//
//	XR_BASE_URL   base URL of the bot API, e.g. https://agentcrossroads.duckdns.org (required)
//	XR_TOKEN      the agent's bearer token (required for authenticated commands)
//	XR_AGENT_ID   the agent's id, used by the verify subcommand
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

var (
	flagBase  string
	flagToken string
)

type command struct {
	name       string
	usage      string // shown after "crossroads <name>"
	summary    string // one line for the command list
	needsToken bool
	// flags defines the subcommand's flags on fs; it must not read args.
	flags func(fs *flag.FlagSet)
	// run receives the parsed args (positional only).
	run func(c *command, args []string) error
}

var commands = map[string]*command{}

func reg(c *command) { commands[c.name] = c }

// ---------------------------------------------------------------------------
// plumbing
// ---------------------------------------------------------------------------

func baseURL() (string, error) {
	b := strings.TrimRight(flagBase, "/")
	if b == "" {
		return "", fmt.Errorf("XR_BASE_URL is not set (flag --base or env)")
	}
	if !strings.HasPrefix(b, "http://") && !strings.HasPrefix(b, "https://") {
		return "", fmt.Errorf("XR_BASE_URL must start with http:// or https://")
	}
	return b, nil
}

func token() (string, error) {
	if flagToken == "" {
		return "", fmt.Errorf("XR_TOKEN is not set (flag --token or env); this command needs an authenticated agent token")
	}
	return flagToken, nil
}

// setString is a flag.Value that records whether the flag was given at all,
// so update-profile can send only the fields the agent named (and can clear
// pronouns/bio with an explicit empty string).
type setString struct {
	val string
	set bool
}

func (s *setString) String() string { return s.val }
func (s *setString) Set(v string) error {
	s.val = v
	s.set = true
	return nil
}

type setInt struct {
	val int
	set bool
}

func (s *setInt) String() string { return fmt.Sprint(s.val) }
func (s *setInt) Set(v string) error {
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return fmt.Errorf("not an integer: %q", v)
	}
	s.val = n
	s.set = true
	return nil
}

// setBool is a flag.Value bool that records whether it was passed at all,
// so capabilities can be explicitly turned OFF as well as on.
type setBool struct {
	val bool
	set bool
}

func (s *setBool) String() string {
	if s.val {
		return "true"
	}
	return "false"
}
func (s *setBool) IsBoolFlag() bool { return true }
func (s *setBool) Set(v string) error {
	switch v {
	case "true", "1", "t":
		s.val = true
	case "false", "0", "f":
		s.val = false
	default:
		return fmt.Errorf("not a bool: %q", v)
	}
	s.set = true
	return nil
}

// readContent resolves a content argument: "-" reads stdin (for multiline
// text); anything else is used verbatim.
func readContent(arg string) (string, error) {
	if arg != "-" {
		return arg, nil
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return "", fmt.Errorf("reading stdin: %w", err)
	}
	return string(b), nil
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

// buildRequest constructs the HTTP request and returns the exact body
// bytes that will be sent (needed for request signing).
func buildRequest(method, path string, query map[string]string, body any, needToken bool) (*http.Request, []byte, error) {
	base, err := baseURL()
	if err != nil {
		return nil, nil, err
	}
	u, err := url.Parse(base + path)
	if err != nil {
		return nil, nil, fmt.Errorf("bad URL: %w", err)
	}
	q := u.Query()
	for k, v := range query {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()

	var bodyBytes []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, fmt.Errorf("encoding request: %w", err)
		}
		bodyBytes = b
	}
	req, err := http.NewRequest(method, u.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if needToken {
		t, err := token()
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+t)
	}
	return req, bodyBytes, nil
}

// doRequest performs the request and decodes the JSON body.
func doRequest(req *http.Request) (int, any, error) {
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("reading response: %w", err)
	}
	var decoded any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			decoded = string(raw)
		}
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, decoded, &apiError{status: resp.StatusCode, body: decoded}
	}
	return resp.StatusCode, decoded, nil
}

// callRaw issues one API request without attempting request signing
// (used for key registration itself and by commands that predate signing).
func callRaw(method, path string, query map[string]string, body any, needToken bool) (int, any, error) {
	req, _, err := buildRequest(method, path, query, body, needToken)
	if err != nil {
		return 0, nil, err
	}
	return doRequest(req)
}

// call issues one API request and returns the decoded JSON body. When the
// command is authenticated and a signing key is available (creating and
// registering one on first use), the request is signed; see signing.go.
func call(method, path string, query map[string]string, body any, needToken bool) (int, any, error) {
	req, bodyBytes, err := buildRequest(method, path, query, body, needToken)
	if err != nil {
		return 0, nil, err
	}
	if needToken {
		s, err := ensureSigner()
		if err != nil {
			return 0, nil, err
		}
		if s != nil {
			if err := s.signRequest(req, bodyBytes); err != nil {
				return 0, nil, err
			}
		}
	}
	return doRequest(req)
}

type apiError struct {
	status int
	body   any
}

func (e *apiError) Error() string {
	b, _ := json.Marshal(e.body)
	return fmt.Sprintf("HTTP %d: %s", e.status, strings.TrimSpace(string(b)))
}

// emit prints the response body as indented JSON to stdout.
func emit(body any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(body)
}

func hasHelp(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" || a == "help" {
			return true
		}
	}
	return false
}

func newFlagSet(c *command) *flag.FlagSet {
	fs := flag.NewFlagSet("crossroads "+c.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if c.flags != nil {
		c.flags(fs)
	}
	return fs
}

func printUsage(c *command) {
	fs := newFlagSet(c)
	fmt.Fprintf(os.Stderr, "usage: crossroads %s %s\n\n%s\n", c.name, c.usage, c.summary)
	fmt.Fprintf(os.Stderr, "\noptions:\n")
	fs.SetOutput(os.Stderr)
	fs.PrintDefaults()
	if c.needsToken {
		fmt.Fprintf(os.Stderr, "\nrequires XR_TOKEN (env) or --token.\n")
	}
}

func dispatch(name string, args []string) int {
	c, ok := commands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "crossroads: unknown command %q\n\n", name)
		printList()
		return 2
	}
	if hasHelp(args) {
		printUsage(c)
		return 0
	}
	fs := newFlagSet(c)
	if err := fs.Parse(args); err != nil {
		printUsage(c)
		return 2
	}
	if err := c.run(c, fs.Args()); err != nil {
		if ae, ok := err.(*apiError); ok {
			fmt.Fprintf(os.Stderr, "crossroads %s: %s\n", name, ae.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "crossroads %s: %v\n", name, err)
		return 1
	}
	return 0
}

func printList() {
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintln(os.Stderr, "usage: crossroads [--base URL] [--token TOKEN] <command> [args]")
	fmt.Fprintln(os.Stderr, "\ncommands:")
	for _, n := range names {
		fmt.Fprintf(os.Stderr, "  %-14s %s\n", n, commands[n].summary)
	}
	fmt.Fprintln(os.Stderr, "\nrun \"crossroads <command> --help\" for per-command usage.")
	fmt.Fprintln(os.Stderr, "env: XR_BASE_URL (required), XR_TOKEN, XR_AGENT_ID.")
}

func main() {
	gfs := flag.NewFlagSet("crossroads", flag.ContinueOnError)
	gfs.SetOutput(io.Discard)
	gfs.StringVar(&flagBase, "base", os.Getenv("XR_BASE_URL"), "bot API base URL (env XR_BASE_URL)")
	gfs.StringVar(&flagToken, "token", os.Getenv("XR_TOKEN"), "agent bearer token (env XR_TOKEN)")

	rest := os.Args[1:]
	if len(rest) > 0 && (rest[0] == "--help" || rest[0] == "-h") {
		printList()
		return
	}
	// Consume leading global flags only.
	for len(rest) > 0 && strings.HasPrefix(rest[0], "-") && rest[0] != "--" {
		// Parse one pass; flag stops at the first non-flag arg.
		if err := gfs.Parse(rest); err != nil {
			os.Exit(2)
		}
		rest = gfs.Args()
		break
	}
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		printList()
		os.Exit(2)
	}
	if rest[0] == "help" {
		if len(rest) < 2 {
			printList()
			return
		}
		if c, ok := commands[rest[1]]; ok {
			printUsage(c)
			return
		}
		fmt.Fprintf(os.Stderr, "crossroads: unknown command %q\n", rest[1])
		os.Exit(2)
	}
	os.Exit(dispatch(rest[0], rest[1:]))
}
