package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
	"github.com/spf13/cobra"
)

func newMetadataCommand(root *rootOptions) *cobra.Command {
	group := &cobra.Command{
		Use: "metadata", Short: "Read and conditionally update freeform card JSON metadata",
		Long: "Read and update arbitrary metadata keys. Keys are literal, including dots and slashes.\nValues are JSON. Null is a value; remove explicitly deletes a key.\nWrites preserve other keys and reject stale revisions instead of silently overwriting them.",
	}
	group.AddCommand(metadataGet(root), metadataSet(root), metadataRemove(root), metadataUpdate(root))
	return group
}

func metadataGet(root *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use: "get CARD_ID [KEY]", Short: "Read metadata and revision, or a single JSON value",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := resolveFormat(cmd, root, formatJSON, formatJSON); err != nil {
				return err
			}
			client, err := newClient(root)
			if err != nil {
				return err
			}
			state, err := client.GetCardMetadata(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if len(args) == 2 {
				value, ok := state.Metadata[args[1]]
				if !ok {
					return fmt.Errorf("metadata key %q does not exist", args[1])
				}
				return outputRawJSON(cmd.OutOrStdout(), value)
			}
			raw, err := json.Marshal(state)
			if err != nil {
				return err
			}
			return outputRawJSON(cmd.OutOrStdout(), raw)
		},
	}
}

func metadataSet(root *rootOptions) *cobra.Command {
	var revision int64
	cmd := &cobra.Command{
		Use: "set CARD_ID KEY JSON_VALUE", Short: "Set one key to a JSON value",
		Example: `  kardbrd card metadata set CARD_ID ops.status '"waiting_external"'
  kardbrd card metadata set CARD_ID attempts 2 --if-revision 7`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			value := json.RawMessage(args[2])
			if !json.Valid(value) {
				return fmt.Errorf("value must be valid JSON; wrap strings in double quotes")
			}
			return writeMetadata(cmd, root, args[0], api.MetadataPatch{
				Set: map[string]json.RawMessage{args[1]: value}, ExpectedRevision: revision,
			})
		},
	}
	metadataRevisionFlag(cmd, &revision)
	return cmd
}

func metadataRemove(root *rootOptions) *cobra.Command {
	var revision int64
	cmd := &cobra.Command{
		Use: "remove CARD_ID KEY [KEY...]", Short: "Remove keys, preserving every other key",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return writeMetadata(cmd, root, args[0], api.MetadataPatch{
				Remove: args[1:], ExpectedRevision: revision,
			})
		},
	}
	metadataRevisionFlag(cmd, &revision)
	return cmd
}

func metadataUpdate(root *rootOptions) *cobra.Command {
	var revision int64
	var setJSON, setFile string
	var remove []string
	cmd := &cobra.Command{
		Use: "update CARD_ID", Short: "Atomically set and remove multiple keys",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("set") && cmd.Flags().Changed("set-file") {
				return fmt.Errorf("--set and --set-file cannot be combined")
			}
			var values map[string]json.RawMessage
			if cmd.Flags().Changed("set-file") {
				var data []byte
				var err error
				if setFile == "-" {
					data, err = io.ReadAll(cmd.InOrStdin())
				} else {
					data, err = os.ReadFile(setFile)
				}
				if err != nil {
					return err
				}
				setJSON = string(data)
			}
			if cmd.Flags().Changed("set") || cmd.Flags().Changed("set-file") {
				if err := json.Unmarshal([]byte(setJSON), &values); err != nil || values == nil {
					return fmt.Errorf("--set or --set-file must contain a JSON object")
				}
			}
			if len(values) == 0 && len(remove) == 0 {
				return fmt.Errorf("provide at least one key with --set, --set-file or --remove")
			}
			for _, key := range remove {
				if _, ok := values[key]; ok {
					return fmt.Errorf("key %q cannot be both set and removed", key)
				}
			}
			return writeMetadata(cmd, root, args[0], api.MetadataPatch{
				Set: values, Remove: remove, ExpectedRevision: revision,
			})
		},
	}
	cmd.Flags().StringVar(&setJSON, "set", "", "JSON object of key/value pairs to set (other keys are preserved)")
	cmd.Flags().StringVar(&setFile, "set-file", "", "Read a JSON object from a file, or - for stdin")
	cmd.Flags().StringArrayVar(&remove, "remove", nil, "Literal key to remove (repeatable)")
	metadataRevisionFlag(cmd, &revision)
	return cmd
}

func metadataRevisionFlag(cmd *cobra.Command, revision *int64) {
	cmd.Flags().Int64Var(revision, "if-revision", 0, "Expected metadata revision; defaults to fetching the current revision once")
}

func writeMetadata(cmd *cobra.Command, root *rootOptions, cardID string, patch api.MetadataPatch) error {
	if _, err := resolveFormat(cmd, root, formatJSON, formatJSON); err != nil {
		return err
	}
	if patch.ExpectedRevision < 0 {
		return fmt.Errorf("--if-revision must be non-negative")
	}
	client, err := newClient(root)
	if err != nil {
		return err
	}
	if !cmd.Flags().Changed("if-revision") {
		state, err := client.GetCardMetadata(cmd.Context(), cardID)
		if err != nil {
			return err
		}
		patch.ExpectedRevision = state.MetadataRevision
	}
	result, err := client.UpdateCardMetadata(cmd.Context(), cardID, patch)
	if err != nil {
		return err
	}
	return outputRawJSON(cmd.OutOrStdout(), result)
}
