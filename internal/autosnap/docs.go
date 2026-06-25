package autosnap

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDocsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "docs",
		Short: "Show installed documentation locations",
		Long: `Show where autosnap documentation is installed.

Packaged installs include manual pages and Markdown documentation. Source builds may
not have those files installed on the local system.`,
		Args: cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "Documentation:")
			fmt.Fprintln(out, "  man autosnap")
			fmt.Fprintln(out, "  man autosnap-<command>")
			fmt.Fprintln(out, "  /usr/share/doc/autosnap/        packaged installs")
			fmt.Fprintln(out, "  /usr/local/share/doc/autosnap/  source installs with make install-docs")
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Run make install-docs after a source build to install manual pages and Markdown docs.")
			return nil
		},
	}
}
