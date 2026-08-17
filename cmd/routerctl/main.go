// routerctl manages a running Codex Subscription Router instance.
//
// It is deliberately a local-only companion to the Windows port: credentials
// remain in the isolated Codex homes and device-code authentication happens in
// the user's browser.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultBaseURL = "http://127.0.0.1:48123"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "routerctl:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	baseURL, tokenPath, remaining, err := commonFlags(args)
	if err != nil {
		return err
	}
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		return fmt.Errorf("read control token %s: %w (start Codex Subscription Router first)", tokenPath, err)
	}
	client := &client{baseURL: strings.TrimRight(baseURL, "/"), token: strings.TrimSpace(string(tokenBytes)), http: &http.Client{Timeout: 30 * time.Second}}
	switch remaining[0] {
	case "list":
		if len(remaining) != 1 {
			return usage()
		}
		return client.call(http.MethodGet, "/v1/accounts", nil)
	case "add":
		flags := flag.NewFlagSet("add", flag.ContinueOnError)
		label := flags.String("label", "", "optional account label")
		if err := flags.Parse(remaining[1:]); err != nil {
			return err
		}
		return client.call(http.MethodPost, "/v1/accounts", map[string]string{"label": *label})
	case "login":
		flags := flag.NewFlagSet("login", flag.ContinueOnError)
		account := flags.String("account", "", "account id from routerctl list")
		if err := flags.Parse(remaining[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*account) == "" {
			return fmt.Errorf("--account is required")
		}
		return client.call(http.MethodPost, "/v1/accounts/"+*account+"/login", map[string]string{"mode": "chatgptDeviceCode"})
	default:
		return usage()
	}
}

func commonFlags(args []string) (string, string, []string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", nil, err
	}
	flags := flag.NewFlagSet("routerctl", flag.ContinueOnError)
	baseURL := flags.String("url", defaultBaseURL, "local router control URL")
	tokenPath := flags.String("token-file", filepath.Join(home, ".codex-mux", "control-token"), "local router token")
	if err := flags.Parse(args); err != nil {
		return "", "", nil, err
	}
	remaining := flags.Args()
	if len(remaining) == 0 {
		return "", "", nil, usage()
	}
	return *baseURL, *tokenPath, remaining, nil
}

type client struct {
	baseURL, token string
	http           *http.Client
}

func (c *client) call(method, path string, body any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("X-Codex-Mux-Token", c.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("contact router: %w", err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("router returned %s: %s", response.Status, strings.TrimSpace(string(contents)))
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, contents, "", "  ") == nil {
		fmt.Println(pretty.String())
		return nil
	}
	fmt.Println(string(contents))
	return nil
}

func usage() error {
	return fmt.Errorf("usage: routerctl [--url URL] [--token-file PATH] list | add [--label LABEL] | login --account ID")
}
