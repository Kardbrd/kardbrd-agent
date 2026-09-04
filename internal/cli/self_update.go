package cli

import (
	"context"
	"fmt"

	"github.com/Kardbrd/kardbrd-agent/internal/update"
	"github.com/spf13/cobra"
)

type selfUpdater interface {
	Update(context.Context) (update.Result, error)
}

var newSelfUpdater = func() selfUpdater {
	return update.New(update.Config{})
}

func newSelfUpdateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "self-update",
		Short: "Fetch and install the latest compatible GitHub release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := newSelfUpdater().Update(cmd.Context())
			if err != nil {
				return fmt.Errorf("self-update: %w", err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Updated kardbrd to %s.\n", result.Tag)
			return err
		},
	}
}
