package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/Kardbrd/kardbrd-agent/internal/version"
	"github.com/spf13/cobra"
)

type rootOptions struct {
	apiURL      string
	token       string
	format      string
	noHeaders   bool
	showVersion bool
}

const (
	formatTSV  = "tsv"
	formatJSON = "json"
	formatMD   = "md"
)

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
	cmd.PersistentFlags().StringVarP(&opts.format, "format", "f", opts.format, "Output format: tsv, json, or md. Row collections default to tsv; other commands default to json.")
	cmd.PersistentFlags().BoolVar(&opts.noHeaders, "no-headers", false, "Suppress the TSV header row.")
	cmd.Flags().BoolVar(&opts.showVersion, "version", false, "Show the version and exit.")
	_ = cmd.RegisterFlagCompletionFunc("format", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{formatTSV, formatJSON, formatMD}, cobra.ShellCompDirectiveDefault
	})

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

func resolveFormat(cmd *cobra.Command, root *rootOptions, fallback string, supported ...string) (string, error) {
	format := fallback
	if flag := cmd.Flags().Lookup("format"); flag != nil && flag.Changed {
		format = root.format
	}
	if !isKnownFormat(format) {
		return "", fmt.Errorf("--format must be one of %s", strings.Join([]string{formatTSV, formatJSON, formatMD}, ", "))
	}
	for _, supportedFormat := range supported {
		if format == supportedFormat {
			return format, nil
		}
	}
	return "", fmt.Errorf("--format %q is not supported by %s; supported formats: %s", format, cmd.CommandPath(), strings.Join(supported, ", "))
}

func isKnownFormat(format string) bool {
	switch format {
	case formatTSV, formatJSON, formatMD:
		return true
	default:
		return false
	}
}
