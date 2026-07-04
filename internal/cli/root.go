package cli

import (
	"fmt"
	"os"

	"github.com/Kardbrd/kardbrd-agent/internal/version"
	"github.com/spf13/cobra"
)

type rootOptions struct {
	apiURL      string
	token       string
	format      string
	showVersion bool
}

func NewRootCommand() *cobra.Command {
	opts := &rootOptions{
		apiURL: envOrDefault("KARDBRD_API_URL", "https://app.kardbrd.com"),
		format: "json",
	}

	cmd := &cobra.Command{
		Use:          "kardbrd",
		Short:        "Kardbrd CLI - interact with Kardbrd boards from the command line.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.showVersion {
				fmt.Fprintf(cmd.OutOrStdout(), "kardbrd version %s\n", version.Version)
				return nil
			}
			return cmd.Help()
		},
	}

	cmd.PersistentFlags().StringVar(&opts.apiURL, "api-url", opts.apiURL, "Kardbrd API base URL.")
	cmd.PersistentFlags().StringVar(&opts.token, "token", opts.token, "Authentication token. [prefer env: KARDBRD_TOKEN]")
	cmd.PersistentFlags().StringVarP(&opts.format, "format", "f", opts.format, "Output format: json or md.")
	cmd.Flags().BoolVar(&opts.showVersion, "version", false, "Show the version and exit.")

	cmd.InitDefaultHelpCmd()
	cmd.InitDefaultCompletionCmd()
	cmd.AddCommand(NewAgentCommand(opts))
	cmd.AddCommand(NewClientCommands(opts)...)

	return cmd
}

func envOrDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
