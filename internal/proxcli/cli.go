// Package proxcli is the prox command line: friendly subcommands over the proxy
// server's flags. Commands that need the server hand back arguments for it;
// everything else finishes here.
package proxcli

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/buildinfo"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
)

const usage = `prox: use all your AI accounts through one local endpoint

  prox start              Run the proxy in this terminal
  prox claude [args]      Open Claude Code through the proxy
  prox login <provider>   Add an account (claude, codex)
  prox accounts           List your accounts
  prox status             Show whether the proxy is running
  prox dashboard          Open the dashboard
  prox service on|off     Run in the background and start at login
  prox upgrade [--force]  Update to the latest release
  prox version            Show the version

Server flags still work, for example: prox -tui
`

// loginFlags maps a provider name to the server flag that signs in to it.
var loginFlags = map[string]string{
	"claude": "-claude-login",
	"codex":  "-codex-login",
}

// Outcome says whether a command finished here or the server should run.
type Outcome struct {
	Done       bool
	ExitCode   int
	ServerArgs []string
	Terminal   *logging.CompactOptions // set when the server should use the tidy terminal log
}

func finished(code int) Outcome { return Outcome{Done: true, ExitCode: code} }

// Dispatch runs one prox command.
func Dispatch(args []string, out, errOut io.Writer) Outcome {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprint(out, usage)
		return finished(0)
	}
	if args[0] == "version" {
		fmt.Fprintf(out, "prox %s (%s)\n", buildinfo.Version, buildinfo.Commit)
		return finished(0)
	}
	paths, err := DefaultPaths()
	if err != nil {
		return fail(errOut, err)
	}
	cfg, err := ensureSetup(paths)
	if err != nil {
		return fail(errOut, err)
	}
	return runCommand(paths, cfg, args, out, errOut)
}

func runCommand(p Paths, cfg settings, args []string, out, errOut io.Writer) Outcome {
	command, rest := args[0], args[1:]
	switch command {
	case "start":
		return startServer(p, cfg, errOut)
	case "login":
		return login(p, rest, errOut)
	case "claude":
		return finished(runClaude(p, cfg, rest, errOut))
	case "dashboard":
		return finished(openDashboard(cfg, out, errOut))
	case "accounts", "status":
		return showAccounts(p, cfg, command, out, errOut)
	case "upgrade":
		return finished(upgrade(cfg, rest, out, errOut))
	case "service":
		return service(p, cfg, rest, out, errOut)
	}
	if command == "discover" {
		return Outcome{ServerArgs: args}
	}
	if strings.HasPrefix(command, "-") {
		return Outcome{ServerArgs: withConfig(p, args)}
	}
	fmt.Fprintf(errOut, "Unknown command %q.\n\n%s", command, usage)
	return finished(2)
}

func startServer(p Paths, cfg settings, errOut io.Writer) Outcome {
	if isReachable(cfg) {
		fmt.Fprintf(errOut, "prox is already running at %s\n", cfg.baseURL())
		return finished(1)
	}
	input, err := startPasswordInput(errOut)
	if err != nil {
		return fail(errOut, err)
	}
	if _, err = requireMasterPassword(p, cfg, input, errOut); err != nil {
		return fail(errOut, err)
	}
	imported, err := importLegacyAccounts(p, cfg)
	if err != nil {
		return fail(errOut, err)
	}
	if imported > 0 {
		fmt.Fprintf(errOut, "Moved %s into prox. %s is no longer used.\n", countAccounts(imported), p.LegacyAuth)
	}
	if err = installPanel(p); err != nil {
		return fail(errOut, err)
	}
	return Outcome{ServerArgs: []string{"-config", p.Config}, Terminal: terminalOptions(p, cfg, os.Stdout)}
}

func login(p Paths, args []string, errOut io.Writer) Outcome {
	if len(args) != 1 {
		fmt.Fprintf(errOut, "Usage: prox login <%s>\n", providerNames())
		return finished(2)
	}
	flag, ok := loginFlags[args[0]]
	if !ok {
		fmt.Fprintf(errOut, "Unknown provider %q. Use one of: %s\n", args[0], providerNames())
		return finished(2)
	}
	// The URL is printed, not opened: a browser's existing session would sign
	// in the same account again, so each account needs a private window.
	fmt.Fprintln(errOut, "Open the link below in a private window to add a different account.")
	return Outcome{ServerArgs: []string{"-config", p.Config, flag, "-no-browser"}}
}

func providerNames() string {
	names := make([]string, 0, len(loginFlags))
	for name := range loginFlags {
		names = append(names, name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

func showAccounts(p Paths, cfg settings, command string, out, errOut io.Writer) Outcome {
	accounts, err := listAccounts(p, cfg)
	if err != nil {
		return fail(errOut, err)
	}
	if command == "status" {
		printStatus(out, cfg, accounts)
	} else {
		printAccounts(out, accounts)
	}
	return finished(0)
}

func service(p Paths, cfg settings, args []string, out, errOut io.Writer) Outcome {
	switch strings.Join(args, " ") {
	case "on":
		return finished(serviceOn(p, cfg, out, errOut))
	case "off":
		return finished(serviceOff(p, out, errOut))
	}
	fmt.Fprintln(errOut, "Usage: prox service on|off")
	return finished(2)
}

func withConfig(p Paths, args []string) []string {
	if slices.Contains(args, "-config") || slices.Contains(args, "--config") {
		return args
	}
	return append([]string{"-config", p.Config}, args...)
}

func fail(errOut io.Writer, err error) Outcome {
	fmt.Fprintln(errOut, err)
	return finished(1)
}
