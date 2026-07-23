package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/auth"
	"github.com/console/tmux-cli/internal/identity"
	"github.com/spf13/cobra"
)

// browserOpener opens a URL in the user's browser. It is a package var so command
// tests can stub it (the default best-effort xdg-open would try to spawn a real
// browser). Failures are ignored by callers — login works headless.
var browserOpener = openBrowser

func openBrowser(url string) error {
	return exec.Command("xdg-open", url).Start()
}

// newAuthSession resolves the backend base URL (cwd setting.yaml → env → default)
// and the XDG-resolved store, returning both plus the base URL for stamping.
func newAuthSession() (*auth.Client, *auth.Store, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, "", err
	}
	base := auth.LoadAPIURL(cwd)
	store, err := auth.NewStore()
	if err != nil {
		return nil, nil, "", err
	}
	return auth.NewClient(base), store, base, nil
}

// runLogin drives the device-code flow to completion and persists the store. It
// prints exactly the two user-facing action lines, then the success line.
func runLogin(ctx context.Context, out io.Writer, c *auth.Client, s *auth.Store, apiURL, cliVersion string) error {
	si := identity.CollectSystemInfo(cliVersion)
	dc, err := c.StartDeviceCode(ctx, auth.ClientMeta{
		Client:      "tmux-cli",
		Fingerprint: si.Fingerprint,
		Hostname:    si.Hostname,
		Version:     cliVersion,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "To authorize this machine, open: %s\n", dc.VerificationURI)
	fmt.Fprintf(out, "Enter code: %s\n", dc.UserCode)
	_ = browserOpener(dc.VerificationURI) // best-effort, silent on failure

	tok, err := c.Poll(ctx, dc.DeviceCode, dc.Interval)
	if err != nil {
		return err
	}
	if err := s.Save(auth.AuthFromToken(apiURL, tok, time.Now())); err != nil {
		return err
	}
	fmt.Fprintf(out, "Logged in as %s (%d scopes)\n", tok.Account, len(tok.Scopes))
	return nil
}

// runLogout deletes auth.json (idempotent) and confirms.
func runLogout(out io.Writer, s *auth.Store) error {
	if err := s.Delete(); err != nil {
		return err
	}
	fmt.Fprintln(out, "Logged out.")
	return nil
}

// runWhoami prints the logged-in identity, refreshing a stale token first, and
// returns the process exit code (1 = not logged in / error, 0 = ok). It is
// factored out of the cobra wrapper so tests capture output without os.Exit.
func runWhoami(ctx context.Context, out, errOut io.Writer, c *auth.Client, s *auth.Store) int {
	a, err := s.Load()
	if err != nil {
		fmt.Fprintf(errOut, "whoami: %v\n", err)
		return 1
	}
	if a == nil {
		fmt.Fprintln(out, "Not logged in — run: tmux-cli login")
		return 1
	}

	a, err = auth.EnsureFresh(ctx, c, s, a)
	if errors.Is(err, auth.ErrReauthRequired) {
		fmt.Fprintln(out, "Not logged in — run: tmux-cli login")
		return 1
	}
	if err != nil {
		fmt.Fprintf(errOut, "whoami: %v\n", err)
		return 1
	}

	wi, err := c.Whoami(ctx, a.AccessToken)
	if err != nil {
		fmt.Fprintf(errOut, "whoami: %v\n", err)
		return 1
	}

	fmt.Fprintf(out, "Account: %s\n", wi.Account)
	fmt.Fprintf(out, "Scopes:  %s\n", strings.Join(wi.Scopes, ", "))
	fmt.Fprintf(out, "Device:  %s\n", wi.DeviceLabel)
	fmt.Fprintf(out, "Expires: %s\n", a.ExpiresAt.Format(time.RFC3339))
	return 0
}

var loginCmd = &cobra.Command{
	Use:           "login",
	Short:         "Authenticate this machine with your tmux-web account (device-code flow)",
	SilenceUsage:  true,
	SilenceErrors: false,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, s, apiURL, err := newAuthSession()
		if err != nil {
			return err
		}
		return runLogin(cmd.Context(), cmd.OutOrStdout(), c, s, apiURL, version)
	},
}

var logoutCmd = &cobra.Command{
	Use:          "logout",
	Short:        "Remove this machine's stored tmux-web credentials",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, s, _, err := newAuthSession()
		if err != nil {
			return err
		}
		return runLogout(cmd.OutOrStdout(), s)
	},
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the logged-in tmux-web account, scopes, device, and token expiry",
	Run: func(cmd *cobra.Command, args []string) {
		c, s, _, err := newAuthSession()
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "whoami: %v\n", err)
			os.Exit(1)
		}
		if code := runWhoami(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), c, s); code != 0 {
			os.Exit(code)
		}
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(whoamiCmd)
}
