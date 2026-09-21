package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/janostudio/heron-connect/api"
	"github.com/janostudio/heron-connect/config"
)

// runShare implements `heron-connect share`, managing public read-only links
// for files in a project work dir.
//
// It talks to the running instance over the same Unix socket the other local
// commands use. Sharing logic lives in the management layer and is reached
// through api.ShareService, so the CLI and the Web UI cannot drift apart —
// path validation, idempotency and revocation have exactly one implementation.
func runShare(args []string) {
	if len(args) == 0 {
		printShareUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "-h", "--help", "help":
		printShareUsage()
		return
	case "list", "ls":
		shareList(args[1:])
	case "revoke", "rm", "del":
		shareRevoke(args[1:])
	default:
		// `share <project> <path>` — create (or reuse) a link.
		shareCreate(args)
	}
}

type shareClient struct {
	http *http.Client
}

// newShareClient dials the local socket, mirroring send.go. Every share
// subcommand needs a running instance; without one there is no store to
// mutate, and silently writing the JSON file behind a live process would leave
// its in-memory copy stale.
func newShareClient(dataDir string) (*shareClient, error) {
	sockPath := resolveSocketPath(dataDir)
	if _, err := os.Stat(sockPath); err != nil {
		return nil, fmt.Errorf("heron-connect is not running (socket not found: %s)", sockPath)
	}
	return &shareClient{
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", sockPath)
				},
			},
		},
	}, nil
}

// call posts to a share endpoint and returns the decoded body. A non-2xx
// response is turned into an error carrying the server's message.
func (c *shareClient) call(path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(data)
	}
	resp, err := c.http.Post("http://unix"+path, "application/json", body)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("%s", msg)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func shareCreate(args []string) {
	var dataDir string
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--data-dir":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --data-dir requires a value")
				os.Exit(1)
			}
			i++
			dataDir = args[i]
		default:
			positional = append(positional, args[i])
		}
	}

	if len(positional) < 2 {
		fmt.Fprintln(os.Stderr, "Error: share requires <project> and <path>")
		printShareUsage()
		os.Exit(1)
	}
	project := positional[0]
	path := positional[1]

	client, err := newShareClient(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var res api.ShareResult
	if err := client.call("/share/create", map[string]string{"project": project, "path": path}, &res); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Only the URL goes to stdout, so `heron-connect share p f | pbcopy` works.
	// The "already shared" note goes to stderr to keep stdout pipeable.
	if res.Reused {
		fmt.Fprintln(os.Stderr, "Already shared — reusing the existing link.")
	}
	fmt.Println(absoluteShareURL(res.URL))
}

func shareList(args []string) {
	var dataDir, project string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--data-dir":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --data-dir requires a value")
				os.Exit(1)
			}
			i++
			dataDir = args[i]
		case "--project", "-p":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --project requires a value")
				os.Exit(1)
			}
			i++
			project = args[i]
		default:
			// Bare positional is the project, matching `share list <project>`.
			if project == "" {
				project = args[i]
			}
		}
	}

	client, err := newShareClient(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	path := "/share/list"
	if project != "" {
		path += "?project=" + urlQueryEscape(project)
	}
	var shares []api.ShareResult
	if err := client.call(path, nil, &shares); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(shares) == 0 {
		fmt.Println("No shared files.")
		return
	}
	for _, sh := range shares {
		fmt.Printf("%s  %s/%s\n", absoluteShareURL(sh.URL), sh.Project, sh.Path)
	}
}

func shareRevoke(args []string) {
	var dataDir string
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--data-dir":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --data-dir requires a value")
				os.Exit(1)
			}
			i++
			dataDir = args[i]
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) == 0 {
		fmt.Fprintln(os.Stderr, "Error: share revoke requires a token")
		printShareUsage()
		os.Exit(1)
	}

	client, err := newShareClient(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Accept a full URL as well as a bare token — the thing users have in hand
	// is the link, so making them strip the prefix would be busywork.
	token := shareTokenFromArg(positional[0])
	if err := client.call("/share/revoke", map[string]string{"token": token}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Share revoked.")
}

// absoluteShareURL turns the server's relative share path into a link the user
// can hand to someone else.
//
// The server cannot know which host a link will be reached through, so the
// host is taken from config (same source `heron-connect web` prints) rather
// than guessed. When config is unreadable the relative path is printed as-is —
// a wrong host is worse than an obviously incomplete link.
func absoluteShareURL(rel string) string {
	if rel == "" {
		return ""
	}
	if strings.HasPrefix(rel, "http://") || strings.HasPrefix(rel, "https://") {
		return rel
	}
	base, ok := shareBaseURL()
	if !ok {
		return rel
	}
	return base + "/" + strings.TrimPrefix(rel, "/")
}

// shareBaseURL builds http://localhost:<mgmt port> from the config file,
// mirroring runWeb's resolution so both commands agree on the port.
func shareBaseURL() (string, bool) {
	configPath := resolveConfigPath("")
	cfg, err := config.Load(configPath)
	if err != nil {
		return "", false
	}
	port := cfg.Management.Port
	if port == 0 {
		port = 9820
	}
	return fmt.Sprintf("http://localhost:%d", port), true
}

// shareTokenFromArg extracts the token from a bare token or a share URL.
//
// Trailing slashes are stripped: browsers and chat clients routinely append
// one, and a token carrying "/" would silently fail to match, making revoke
// look broken.
func shareTokenFromArg(arg string) string {
	arg = strings.TrimSpace(arg)
	// Drop any query/fragment first — they are not part of the token.
	if i := strings.IndexAny(arg, "?#"); i >= 0 {
		arg = arg[:i]
	}
	arg = strings.TrimRight(arg, "/")
	if i := strings.LastIndex(arg, "/share/"); i >= 0 {
		return arg[i+len("/share/"):]
	}
	if i := strings.LastIndex(arg, "/"); i >= 0 {
		return arg[i+1:]
	}
	return arg
}

// urlQueryEscape escapes a query value without pulling in net/url here for one
// call site. Escaping the separators that matter is enough for a project name.
func urlQueryEscape(s string) string {
	r := strings.NewReplacer(
		"%", "%25", "&", "%26", "=", "%3D", "+", "%2B",
		"?", "%3F", "#", "%23", " ", "%20", "/", "%2F",
	)
	return r.Replace(s)
}

func printShareUsage() {
	fmt.Println(`Usage: heron-connect share <project> <path>
       heron-connect share list [project]
       heron-connect share revoke <token|url>

Create or manage public read-only links to a file in a project work dir.
Anyone holding a link can read that one file without logging in.

Creating is idempotent: asking to share a file that is already shared returns
the existing link instead of minting a second one.

Commands:
  share <project> <path>    Share a file; prints the link (new or existing)
  share list [project]      List share links
  share revoke <token|url>  Disable a link immediately

Options:
  --data-dir <path>    Data directory (default: ~/.heron-connect)
  -h, --help           Show this help

Examples:
  heron-connect share auto-bugfix reports/q3.md
  heron-connect share auto-bugfix reports/q3.md | pbcopy
  heron-connect share list auto-bugfix
  heron-connect share revoke https://host/api/v1/share/ab12...`)
}
