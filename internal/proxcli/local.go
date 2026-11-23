package proxcli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/localmodels"
)

const localUsage = `prox local: models that run on this machine, no sign-in needed

  prox local                  List the local models and which are installed
  prox local install <model>  Download a model and its runtime (needs uv)
  prox local remove <model>   Delete a model from this machine

Installed decision models answer at POST /v1/systemone (the Jev / System One API).
`

// local runs `prox local …`.
func local(p Paths, args []string, out, errOut io.Writer) int {
	layout := localmodels.LayoutIn(p.Home)
	if len(args) == 0 || args[0] == "list" {
		listLocalModels(layout, out)
		return 0
	}
	if len(args) != 2 || (args[0] != "install" && args[0] != "remove") {
		fmt.Fprint(errOut, localUsage)
		return 2
	}
	m, ok := localmodels.Find(args[1])
	if !ok {
		fmt.Fprintf(errOut, "Unknown local model %q. Run `prox local` to see them.\n", args[1])
		return 2
	}
	if args[0] == "install" {
		return installLocalModel(layout, m, out, errOut)
	}
	return removeLocalModel(layout, m, out, errOut)
}

func listLocalModels(layout localmodels.Layout, out io.Writer) {
	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tPROVIDER\tSIZE\tSTATUS")
	for _, m := range localmodels.Catalog() {
		status := "not installed"
		if layout.Installed(m) {
			status = "installed"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.ID, m.ProviderName, m.DownloadSize, status)
	}
	_ = tw.Flush()
}

func installLocalModel(layout localmodels.Layout, m localmodels.Model, out, errOut io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(out, "Installing %s from %s (about %s) into %s\n", m.ID, m.ProviderName, m.DownloadSize, layout.ModelDir(m))
	installer := localmodels.NewInstaller(layout, func(step string) { fmt.Fprintf(out, "  %s\n", step) })
	if err := installer.Install(ctx, m); err != nil {
		fmt.Fprintf(errOut, "Install failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "Ready. Ask it with POST /v1/systemone and \"model\": %q.\n", m.ID)
	return 0
}

func removeLocalModel(layout localmodels.Layout, m localmodels.Model, out, errOut io.Writer) int {
	if err := layout.Remove(m); err != nil {
		fmt.Fprintf(errOut, "Remove failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "Removed %s. A running prox frees its memory when it restarts.\n", m.ID)
	return 0
}
