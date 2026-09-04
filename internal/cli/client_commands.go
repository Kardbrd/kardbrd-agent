package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
	"github.com/spf13/cobra"
)

func NewClientCommands(root *rootOptions) []*cobra.Command {
	return []*cobra.Command{
		newActivityCommand(root),
		newAttachmentCommand(root),
		newBoardCommand(root),
		newCardCommand(root),
		newChecklistCommand(root),
		newCommentCommand(root),
		newLinkCommand(root),
		newListCommand(root),
		newMDCommand(root),
		newSearchCommand(root),
	}
}

func newClient(root *rootOptions) (*api.Client, error) {
	apiURL := root.apiURL
	if apiURL == "" {
		apiURL = envOrDefault("KARDBRD_API_URL", "https://app.kardbrd.com")
	}
	token := root.token
	if token == "" {
		token = os.Getenv("KARDBRD_TOKEN")
	}
	if apiURL == "" {
		return nil, fmt.Errorf("--api-url or KARDBRD_API_URL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("--token or KARDBRD_TOKEN is required")
	}
	return api.NewClient(apiURL, token), nil
}

func runJSON(cmd *cobra.Command, root *rootOptions, call func(context.Context, *api.Client) (json.RawMessage, error)) error {
	if _, err := resolveFormat(cmd, root, formatJSON, formatJSON); err != nil {
		return err
	}
	client, err := newClient(root)
	if err != nil {
		return err
	}
	raw, err := call(cmd.Context(), client)
	if err != nil {
		return err
	}
	return outputRawJSON(cmd.OutOrStdout(), raw)
}

func runMarkdown(cmd *cobra.Command, root *rootOptions, call func(context.Context, *api.Client) (string, error)) error {
	if _, err := resolveFormat(cmd, root, formatMD, formatMD); err != nil {
		return err
	}
	client, err := newClient(root)
	if err != nil {
		return err
	}
	markdown, err := call(cmd.Context(), client)
	if err != nil {
		return err
	}
	return outputMarkdown(cmd.OutOrStdout(), markdown)
}

func runCollection(cmd *cobra.Command, root *rootOptions, schema []tableColumn, call func(context.Context, *api.Client) (json.RawMessage, error)) error {
	format, err := resolveFormat(cmd, root, formatTSV, formatTSV, formatJSON, formatMD)
	if err != nil {
		return err
	}
	client, err := newClient(root)
	if err != nil {
		return err
	}
	raw, err := call(cmd.Context(), client)
	if err != nil {
		return err
	}
	if format == formatJSON {
		return outputRawJSON(cmd.OutOrStdout(), raw)
	}
	table, err := tableFromJSON(raw, schema)
	if err != nil {
		return err
	}
	if format == formatMD {
		return outputTableMarkdown(cmd.OutOrStdout(), table)
	}
	return outputTableTSV(cmd.OutOrStdout(), table, root.noHeaders)
}

func collectionCommand(use string, short string, args cobra.PositionalArgs, root *rootOptions, schema []tableColumn, build func(*cobra.Command, []string) func(context.Context, *api.Client) (json.RawMessage, error)) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  args,
		RunE: func(cmd *cobra.Command, argv []string) error {
			return runCollection(cmd, root, schema, build(cmd, argv))
		},
	}
}

func collectionField(raw json.RawMessage, key string) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	if value, ok := object[key]; ok {
		return value, nil
	}
	return json.RawMessage(`[]`), nil
}

func tableSchema(fields ...tableColumn) []tableColumn {
	return fields
}

func column(header string, paths ...string) tableColumn {
	column := tableColumn{header: header, paths: make([][]string, len(paths))}
	for i, path := range paths {
		column.paths[i] = strings.Split(path, ".")
	}
	return column
}

func optionalColumn(header string, paths ...string) tableColumn {
	column := column(header, paths...)
	column.optional = true
	return column
}

var (
	boardsTableSchema = tableSchema(
		column("id", "id"),
		column("name", "name"),
		column("workspace_id", "workspace_id", "workspace.id"),
		column("workspace_name", "workspace_name", "workspace.name"),
		column("description", "description"),
		column("created_at", "created_at"),
		column("updated_at", "updated_at"),
	)
	membersTableSchema = tableSchema(
		column("id", "id"),
		column("display_name", "display_name", "name"),
		column("email", "email"),
		column("is_bot", "is_bot"),
	)
	labelsTableSchema = tableSchema(
		column("id", "id"),
		column("name", "name"),
		column("color", "color"),
		column("position", "position"),
	)
	attachmentsTableSchema = tableSchema(
		column("id", "id"),
		column("filename", "filename"),
		column("file_size", "file_size"),
		column("file_size_display", "file_size_display"),
		column("content_type", "content_type"),
		column("created_at", "created_at"),
	)
	linksTableSchema = tableSchema(
		column("id", "id"),
		column("display_text", "display_text"),
		column("url", "url"),
		column("position", "position"),
		column("created_by_id", "created_by_id", "created_by.id"),
		column("created_by_name", "created_by_name", "created_by.name", "created_by.display_name"),
		column("created_at", "created_at"),
		column("updated_at", "updated_at"),
	)
	boardSearchTableSchema = tableSchema(
		column("id", "id"),
		column("title", "title"),
		column("list_name", "list_name", "list.name"),
	)
	searchTableSchema = tableSchema(
		column("id", "id"),
		column("title", "title"),
		column("board_id", "board_id", "board.id"),
		column("board_name", "board_name", "board.name"),
		column("workspace_id", "workspace_id", "workspace.id"),
		column("workspace_name", "workspace_name", "workspace.name"),
		column("list_name", "list_name", "list.name"),
		column("is_archived", "is_archived"),
		column("match_locations", "match_locations"),
		column("updated_at", "updated_at"),
	)
	activityTableSchema = tableSchema(
		column("id", "id"),
		column("created_at", "created_at"),
		column("user_id", "user_id", "user.id"),
		column("user_name", "user_name", "user.name", "user.display_name"),
		column("via_agent", "via_agent"),
		column("action", "action"),
		column("entity_type", "entity_type"),
		column("entity_id", "entity_id"),
		column("entity_name", "entity_name"),
		column("card_id", "card_id", "card.id"),
		column("card_title", "card_title", "card.title"),
		column("board_id", "board_id", "board.id"),
		optionalColumn("board_name", "board_name", "board.name"),
		optionalColumn("workspace_id", "workspace_id", "workspace.id"),
		optionalColumn("workspace_name", "workspace_name", "workspace.name"),
	)
)

func newMDCommand(root *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "md {board|card|boards|activity} [ID]",
		Short: "Shortcut for getting a resource in markdown format",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("resource is required")
			}
			switch args[0] {
			case "boards":
				return nil
			case "board", "card", "activity":
				if len(args) < 2 {
					return fmt.Errorf("%s ID is required", strings.ToUpper(args[0]))
				}
				return nil
			default:
				return fmt.Errorf("resource must be one of board, card, boards, activity")
			}
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "boards":
				return runMarkdown(cmd, root, func(ctx context.Context, client *api.Client) (string, error) {
					return client.ListBoardsMarkdown(ctx)
				})
			case "board":
				return runMarkdown(cmd, root, func(ctx context.Context, client *api.Client) (string, error) {
					return client.GetBoardMarkdown(ctx, args[1], false)
				})
			case "card":
				return runMarkdown(cmd, root, func(ctx context.Context, client *api.Client) (string, error) {
					return client.GetCardMarkdown(ctx, args[1])
				})
			case "activity":
				return runMarkdown(cmd, root, func(ctx context.Context, client *api.Client) (string, error) {
					return client.GetBoardActivityMarkdown(ctx, args[1], api.ActivityOptions{Limit: 50})
				})
			}
			return nil
		},
	}
}

func newBoardCommand(root *rootOptions) *cobra.Command {
	group := &cobra.Command{Use: "board", Short: "Board operations"}
	group.AddCommand(boardGet(root), boardList(root), boardLabels(root), boardActivity(root), boardMembers(root), boardUpdate(root), boardArchive(root), boardUnarchive(root), boardFavorite(root), boardSearch(root))
	return group
}

func boardGet(root *rootOptions) *cobra.Command {
	var includeArchived bool
	cmd := &cobra.Command{
		Use:   "get BOARD_ID",
		Short: "Get board details including lists, cards, and members",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if root.format == "md" {
				return runMarkdown(cmd, root, func(ctx context.Context, client *api.Client) (string, error) {
					return client.GetBoardMarkdown(ctx, args[0], includeArchived)
				})
			}
			return runJSON(cmd, root, func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
				return client.GetBoard(ctx, args[0], includeArchived)
			})
		},
	}
	cmd.Flags().BoolVar(&includeArchived, "include-archived", false, "Include archived cards")
	return cmd
}

func boardList(root *rootOptions) *cobra.Command {
	return collectionCommand("list", "List all accessible boards", cobra.NoArgs, root, boardsTableSchema, func(_ *cobra.Command, _ []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.ListBoards(ctx)
		}
	})
}

func boardLabels(root *rootOptions) *cobra.Command {
	return collectionCommand("labels BOARD_ID", "Get all labels defined on a board", cobra.ExactArgs(1), root, labelsTableSchema, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.GetBoardLabels(ctx, args[0])
		}
	})
}

func boardActivity(root *rootOptions) *cobra.Command {
	var limit int
	var since string
	cmd := &cobra.Command{
		Use:   "activity BOARD_ID",
		Short: "Get recent activity on a board",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := api.ActivityOptions{Limit: limit, Since: since}
			return runCollection(cmd, root, activityTableSchema, func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
				return client.GetBoardActivity(ctx, args[0], opts)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "Max activities to return")
	cmd.Flags().StringVar(&since, "since", "", "ISO 8601 timestamp to filter after")
	return cmd
}

func boardMembers(root *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "members BOARD_ID",
		Short: "List all members of a board",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCollection(cmd, root, membersTableSchema, func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
				raw, err := client.GetBoard(ctx, args[0], false)
				if err != nil {
					return nil, err
				}
				return collectionField(raw, "members")
			})
		},
	}
}

func boardUpdate(root *rootOptions) *cobra.Command {
	var name string
	cmd := jsonCommand("update BOARD_ID", "Update a board's name", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.UpdateBoard(ctx, args[0], expandPublishedText(name))
		}
	})
	cmd.Flags().StringVar(&name, "name", "", "New board name")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func boardArchive(root *rootOptions) *cobra.Command {
	return jsonCommand("archive BOARD_ID", "Archive a board", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.ArchiveBoard(ctx, args[0])
		}
	})
}

func boardUnarchive(root *rootOptions) *cobra.Command {
	return jsonCommand("unarchive BOARD_ID", "Unarchive a board", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.UnarchiveBoard(ctx, args[0])
		}
	})
}

func boardFavorite(root *rootOptions) *cobra.Command {
	return jsonCommand("favorite BOARD_ID", "Toggle favorite status for a board", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.ToggleBoardFavorite(ctx, args[0])
		}
	})
}

func boardSearch(root *rootOptions) *cobra.Command {
	var limit int
	cmd := collectionCommand("search BOARD_ID QUERY", "Search cards on a board by title", cobra.ExactArgs(2), root, boardSearchTableSchema, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.BoardCardSearch(ctx, args[0], args[1], limit)
		}
	})
	cmd.Flags().IntVar(&limit, "limit", 10, "Max results")
	return cmd
}

func newCardCommand(root *rootOptions) *cobra.Command {
	group := &cobra.Command{Use: "card", Short: "Card operations"}
	group.AddCommand(cardGet(root), cardCreate(root), cardUpdate(root), cardMove(root), cardArchive(root), cardUnarchive(root), cardAssign(root), cardUnassign(root), cardActivity(root), cardMoveToBoard(root))
	return group
}

func cardGet(root *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "get CARD_ID",
		Short: "Get card details including checklists, comments, and metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if root.format == "md" {
				return runMarkdown(cmd, root, func(ctx context.Context, client *api.Client) (string, error) {
					return client.GetCardMarkdown(ctx, args[0])
				})
			}
			return runJSON(cmd, root, func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
				return client.GetCard(ctx, args[0])
			})
		},
	}
}

func cardCreate(root *rootOptions) *cobra.Command {
	var boardID, listID, title, description string
	cmd := jsonCommand("create", "Create a new card in a list", cobra.NoArgs, root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.CreateCard(ctx, boardID, listID, expandPublishedText(title), expandPublishedText(description))
		}
	})
	cmd.Flags().StringVar(&boardID, "board", "", "Board ID")
	cmd.Flags().StringVar(&listID, "list", "", "List ID")
	cmd.Flags().StringVar(&title, "title", "", "Card title")
	cmd.Flags().StringVar(&description, "description", "", "Card description")
	_ = cmd.MarkFlagRequired("board")
	_ = cmd.MarkFlagRequired("list")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

func cardUpdate(root *rootOptions) *cobra.Command {
	var title, description, dueDate, assigneeID string
	var labelIDs []string
	var clearLabels bool
	cmd := &cobra.Command{
		Use:   "update CARD_ID",
		Short: "Update a card's fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			labelReplace := cmd.Flags().Changed("label") || cmd.Flags().Changed("label-ids") || clearLabels
			if clearLabels && (cmd.Flags().Changed("label") || cmd.Flags().Changed("label-ids")) {
				return fmt.Errorf("--clear-labels cannot be combined with --label or --label-ids")
			}

			patch := buildCardPatch(expandPublishedText(title), expandPublishedText(description), dueDate, assigneeID)
			if !hasCardPatchUpdates(patch) && !labelReplace {
				return fmt.Errorf("at least one update flag is required")
			}

			client, err := newClient(root)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if !labelReplace {
				raw, err := client.UpdateCard(ctx, args[0], patch)
				if err != nil {
					return err
				}
				return outputRawJSON(cmd.OutOrStdout(), raw)
			}

			state, err := client.GetCardLabelState(ctx, args[0])
			if err != nil {
				return err
			}
			catalog, err := client.GetBoardLabelCatalog(ctx, state.Board.ID)
			if err != nil {
				return err
			}
			desiredLabelIDs := labelIDs
			if clearLabels {
				desiredLabelIDs = nil
			}
			plan, err := reconcileLabelIDs(labelIDsFromLabels(state.Labels), labelIDsFromLabels(catalog.Labels), desiredLabelIDs)
			if err != nil {
				return err
			}

			if hasCardPatchUpdates(patch) {
				if _, err := client.UpdateCard(ctx, args[0], patch); err != nil {
					return fmt.Errorf("card scalar update failed before label reconciliation: %w", err)
				}
			}
			for _, labelID := range plan.Additions {
				if err := client.AddCardLabel(ctx, args[0], labelID); err != nil {
					return fmt.Errorf("label reconciliation incomplete while adding %q; no removals attempted: %w", labelID, err)
				}
			}
			for _, labelID := range plan.Removals {
				if err := client.RemoveCardLabel(ctx, args[0], labelID); err != nil {
					return fmt.Errorf("label reconciliation incomplete while removing %q: %w", labelID, err)
				}
			}
			raw, err := client.GetCard(ctx, args[0])
			if err != nil {
				return fmt.Errorf("label reconciliation completed but final card refresh failed: %w", err)
			}
			return outputRawJSON(cmd.OutOrStdout(), raw)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "New title")
	cmd.Flags().StringVar(&description, "description", "", "New description")
	cmd.Flags().StringVar(&dueDate, "due", "", "Due date")
	cmd.Flags().StringVar(&assigneeID, "assignee", "", "Assignee user ID")
	cmd.Flags().StringArrayVar(&labelIDs, "label", nil, "Label ID in the complete desired label set; repeat for each label")
	cmd.Flags().StringArrayVar(&labelIDs, "label-ids", nil, "Label ID in the complete desired label set; repeat for each label")
	cmd.Flags().BoolVar(&clearLabels, "clear-labels", false, "Replace the card's labels with an empty set")
	return cmd
}

func cardMove(root *rootOptions) *cobra.Command {
	var listID string
	var position int
	cmd := jsonCommand("move CARD_ID", "Move a card to a different list", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			var pos *int
			if position >= 0 {
				pos = &position
			}
			return client.MoveCard(ctx, args[0], listID, pos)
		}
	})
	cmd.Flags().StringVar(&listID, "list", "", "Target list ID")
	cmd.Flags().IntVar(&position, "position", -1, "Position in list")
	_ = cmd.MarkFlagRequired("list")
	return cmd
}

func cardArchive(root *rootOptions) *cobra.Command {
	return jsonCommand("archive CARD_ID", "Archive a card", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.ArchiveCard(ctx, args[0])
		}
	})
}

func cardUnarchive(root *rootOptions) *cobra.Command {
	return jsonCommand("unarchive CARD_ID", "Restore an archived card", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.UnarchiveCard(ctx, args[0])
		}
	})
}

func cardAssign(root *rootOptions) *cobra.Command {
	return jsonCommand("assign CARD_ID USER_ID", "Assign a board member to a card", cobra.ExactArgs(2), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			assignee := args[1]
			return client.UpdateCard(ctx, args[0], api.CardPatch{AssigneeID: &assignee, AssigneeSet: true})
		}
	})
}

func cardUnassign(root *rootOptions) *cobra.Command {
	return jsonCommand("unassign CARD_ID", "Remove the assignee from a card", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.UpdateCard(ctx, args[0], api.CardPatch{AssigneeSet: true})
		}
	})
}

func cardActivity(root *rootOptions) *cobra.Command {
	var limit int
	var since string
	cmd := collectionCommand("activity CARD_ID", "Get recent activity on a card", cobra.ExactArgs(1), root, activityTableSchema, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.GetCardActivity(ctx, args[0], api.ActivityOptions{Limit: limit, Since: since})
		}
	})
	cmd.Flags().IntVar(&limit, "limit", 20, "Max activities to return")
	cmd.Flags().StringVar(&since, "since", "", "ISO 8601 timestamp to filter after")
	return cmd
}

func cardMoveToBoard(root *rootOptions) *cobra.Command {
	var boardID string
	cmd := jsonCommand("move-to-board CARD_ID", "Move a card to a different board", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.MoveCardToBoard(ctx, args[0], boardID)
		}
	})
	cmd.Flags().StringVar(&boardID, "board", "", "Target board ID")
	_ = cmd.MarkFlagRequired("board")
	return cmd
}

func newCommentCommand(root *rootOptions) *cobra.Command {
	group := &cobra.Command{Use: "comment", Short: "Comment operations on cards"}
	group.AddCommand(
		jsonCommand("add CARD_ID MESSAGE", "Add a comment to a card", cobra.ExactArgs(2), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
			return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
				return client.AddComment(ctx, args[0], expandPublishedText(args[1]))
			}
		}),
		&cobra.Command{
			Use:   "delete CARD_ID COMMENT_ID",
			Short: "Delete a comment",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				if _, err := resolveFormat(cmd, root, formatJSON, formatJSON); err != nil {
					return err
				}
				client, err := newClient(root)
				if err != nil {
					return err
				}
				if err := client.DeleteComment(cmd.Context(), args[0], args[1]); err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "Comment deleted.")
				return err
			},
		},
		jsonCommand("react CARD_ID COMMENT_ID EMOJI", "Toggle a reaction emoji on a comment", cobra.ExactArgs(3), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
			return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
				return client.ToggleReaction(ctx, args[0], args[1], args[2])
			}
		}),
	)
	return group
}

func newChecklistCommand(root *rootOptions) *cobra.Command {
	group := &cobra.Command{Use: "checklist", Short: "Checklist and todo item operations"}
	group.AddCommand(checklistCreate(root), checklistAddTodo(root), checklistAddTodos(root), checklistUpdate(root), checklistComplete(root), checklistReopen(root), checklistExtract(root))
	return group
}

func checklistCreate(root *rootOptions) *cobra.Command {
	var title string
	cmd := jsonCommand("create CARD_ID", "Create a new checklist on a card", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.CreateChecklist(ctx, args[0], expandPublishedText(title))
		}
	})
	cmd.Flags().StringVar(&title, "title", "", "Checklist title")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

func checklistAddTodo(root *rootOptions) *cobra.Command {
	var checklistID, title string
	cmd := jsonCommand("add-todo CARD_ID", "Add a todo item to a checklist", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.AddTodo(ctx, args[0], checklistID, expandPublishedText(title))
		}
	})
	cmd.Flags().StringVar(&checklistID, "checklist", "", "Checklist ID")
	cmd.Flags().StringVar(&title, "title", "", "Todo item title")
	_ = cmd.MarkFlagRequired("checklist")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

func checklistAddTodos(root *rootOptions) *cobra.Command {
	var title string
	cmd := jsonCommand("add-todos CARD_ID ITEMS...", "Create a checklist with multiple items at once", cobra.MinimumNArgs(2), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.AddTodos(ctx, args[0], expandPublishedText(title), expandPublishedTextSlice(args[1:]))
		}
	})
	cmd.Flags().StringVar(&title, "title", "", "Checklist title")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

func checklistUpdate(root *rootOptions) *cobra.Command {
	var checklistID, itemID, title, dueDate string
	var completed bool
	var noCompleted bool
	var completedSet bool
	var assignees []string
	cmd := jsonCommand("update CARD_ID", "Update a todo item", cobra.ExactArgs(1), root, func(cmd *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			patch, err := buildTodoPatch(expandPublishedText(title), dueDate, completed, completedSet, assignees)
			if err != nil {
				return nil, err
			}
			return client.UpdateTodo(ctx, args[0], checklistID, itemID, patch)
		}
	})
	cmd.Flags().StringVar(&checklistID, "checklist", "", "Checklist ID")
	cmd.Flags().StringVar(&itemID, "item", "", "Todo item ID")
	cmd.Flags().StringVar(&title, "title", "", "New title")
	cmd.Flags().BoolVar(&completed, "completed", false, "Completion status")
	cmd.Flags().BoolVar(&noCompleted, "no-completed", false, "Mark incomplete")
	cmd.Flags().BoolVar(&completedSet, "completed-set", false, "internal completion sentinel")
	_ = cmd.Flags().MarkHidden("completed-set")
	cmd.Flags().Lookup("completed").NoOptDefVal = "true"
	cmd.Flags().StringVar(&dueDate, "due", "", "Due date")
	cmd.Flags().StringArrayVar(&assignees, "assignee", nil, "Assignee user IDs")
	_ = cmd.MarkFlagRequired("checklist")
	_ = cmd.MarkFlagRequired("item")
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("completed") && cmd.Flags().Changed("no-completed") {
			return fmt.Errorf("--completed and --no-completed cannot be used together")
		}
		completedSet = cmd.Flags().Changed("completed")
		if cmd.Flags().Changed("no-completed") {
			completed = false
			completedSet = true
		}
		return nil
	}
	return cmd
}

func checklistComplete(root *rootOptions) *cobra.Command {
	return jsonCommand("complete CARD_ID TODO_ID", "Mark a todo item as completed", cobra.ExactArgs(2), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.CompleteTodo(ctx, args[0], args[1])
		}
	})
}

func checklistReopen(root *rootOptions) *cobra.Command {
	return jsonCommand("reopen CARD_ID TODO_ID", "Reopen a completed todo item", cobra.ExactArgs(2), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.ReopenTodo(ctx, args[0], args[1])
		}
	})
}

func checklistExtract(root *rootOptions) *cobra.Command {
	var targetListID, checklistID, prefix string
	cmd := jsonCommand("extract CARD_ID", "Extract todos into separate cards", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			if checklistID != "" {
				return client.ExtractChecklistToCards(ctx, args[0], checklistID, targetListID, expandPublishedText(prefix))
			}
			return client.ExtractTodosToCards(ctx, args[0], targetListID, expandPublishedText(prefix))
		}
	})
	cmd.Flags().StringVar(&targetListID, "target-list", "", "Target list ID")
	cmd.Flags().StringVar(&checklistID, "checklist", "", "Checklist ID")
	cmd.Flags().StringVar(&prefix, "prefix", "", "Prefix for new card titles")
	_ = cmd.MarkFlagRequired("target-list")
	return cmd
}

func newAttachmentCommand(root *rootOptions) *cobra.Command {
	group := &cobra.Command{Use: "attachment", Short: "Attachment operations on cards"}
	group.AddCommand(attachmentUpload(root), attachmentMarkdown(root), attachmentList(root), attachmentGet(root))
	return group
}

func attachmentUpload(root *rootOptions) *cobra.Command {
	return jsonCommand("upload CARD_ID FILE_PATH", "Upload a file to a card", cobra.ExactArgs(2), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.UploadAttachment(ctx, args[0], args[1])
		}
	})
}

func attachmentMarkdown(root *rootOptions) *cobra.Command {
	var filename, content, contentFile string
	cmd := jsonCommand("markdown CARD_ID", "Upload markdown content as an attachment", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			text, err := contentFromFlags(content, contentFile, os.Stdin)
			if err != nil {
				return nil, err
			}
			return client.UploadMarkdownContent(ctx, args[0], filename, text)
		}
	})
	cmd.Flags().StringVar(&filename, "filename", "", "Filename for the markdown attachment")
	cmd.Flags().StringVar(&content, "content", "", "Markdown content")
	cmd.Flags().StringVar(&contentFile, "content-file", "", "Read content from file, or - for stdin")
	_ = cmd.MarkFlagRequired("filename")
	return cmd
}

func attachmentList(root *rootOptions) *cobra.Command {
	return collectionCommand("list CARD_ID", "List all attachments on a card", cobra.ExactArgs(1), root, attachmentsTableSchema, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.ListAttachments(ctx, args[0])
		}
	})
}

func attachmentGet(root *rootOptions) *cobra.Command {
	return jsonCommand("get CARD_ID ATTACHMENT_ID", "Download an attachment", cobra.ExactArgs(2), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.GetAttachment(ctx, args[0], args[1])
		}
	})
}

func newLinkCommand(root *rootOptions) *cobra.Command {
	group := &cobra.Command{Use: "link", Short: "Link operations on cards"}
	group.AddCommand(
		linkAdd(root),
		linkList(root),
		linkUpdate(root),
		linkDelete(root),
	)
	return group
}

func linkList(root *rootOptions) *cobra.Command {
	return collectionCommand("list CARD_ID", "List all links on a card", cobra.ExactArgs(1), root, linksTableSchema, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.ListCardLinks(ctx, args[0])
		}
	})
}

func linkAdd(root *rootOptions) *cobra.Command {
	var displayText string
	cmd := jsonCommand("add CARD_ID URL", "Add a URL link to a card", cobra.ExactArgs(2), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.AddCardLink(ctx, args[0], args[1], expandPublishedText(displayText))
		}
	})
	cmd.Flags().StringVar(&displayText, "text", "", "Display text for the link")
	return cmd
}

func linkUpdate(root *rootOptions) *cobra.Command {
	var linkURL, displayText string
	cmd := jsonCommand("update CARD_ID LINK_ID", "Update a link", cobra.ExactArgs(2), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			patch, err := buildLinkPatch(linkURL, expandPublishedText(displayText))
			if err != nil {
				return nil, err
			}
			return client.UpdateCardLink(ctx, args[0], args[1], patch)
		}
	})
	cmd.Flags().StringVar(&linkURL, "url", "", "New URL")
	cmd.Flags().StringVar(&displayText, "text", "", "New display text")
	return cmd
}

func linkDelete(root *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "delete CARD_ID LINK_ID",
		Short: "Delete a link",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := resolveFormat(cmd, root, formatJSON, formatJSON); err != nil {
				return err
			}
			client, err := newClient(root)
			if err != nil {
				return err
			}
			if err := client.DeleteCardLink(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Link deleted.")
			return err
		},
	}
}

func newSearchCommand(root *rootOptions) *cobra.Command {
	var workspace string
	var includeArchived bool
	var limit, offset int
	cmd := collectionCommand("search QUERY", "Search cards across all accessible boards", cobra.ExactArgs(1), root, searchTableSchema, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.Search(ctx, args[0], api.SearchOptions{Workspace: workspace, IncludeArchived: includeArchived, Limit: limit, Offset: offset})
		}
	})
	cmd.Flags().StringVar(&workspace, "workspace", "", "Filter to workspace by ID")
	cmd.Flags().BoolVar(&includeArchived, "include-archived", false, "Include archived cards")
	cmd.Flags().IntVar(&limit, "limit", 30, "Max results")
	cmd.Flags().IntVar(&offset, "offset", 0, "Pagination offset")
	return cmd
}

func newActivityCommand(root *rootOptions) *cobra.Command {
	var opts api.ActivityOptions
	cmd := collectionCommand("activity", "Get cross-board activity feed", cobra.NoArgs, root, activityTableSchema, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.GetActivity(ctx, opts)
		}
	})
	cmd.Flags().IntVar(&opts.Limit, "limit", 30, "Max activities to return")
	cmd.Flags().StringVar(&opts.Since, "since", "", "ISO 8601 timestamp to filter after")
	cmd.Flags().StringVar(&opts.Actor, "actor", "", "Filter by actor type")
	cmd.Flags().StringVar(&opts.Source, "source", "", "Filter by source")
	cmd.Flags().StringVar(&opts.Period, "period", "", "Time period filter")
	cmd.Flags().StringVar(&opts.Board, "board", "", "Filter by board ID")
	return cmd
}

func newListCommand(root *rootOptions) *cobra.Command {
	group := &cobra.Command{Use: "list", Short: "List operations on boards"}
	group.AddCommand(listCreate(root), listMove(root))
	return group
}

func listCreate(root *rootOptions) *cobra.Command {
	var name string
	cmd := jsonCommand("create BOARD_ID", "Create a new list on a board", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.CreateList(ctx, args[0], expandPublishedText(name))
		}
	})
	cmd.Flags().StringVar(&name, "name", "", "List name")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func listMove(root *rootOptions) *cobra.Command {
	var position int
	cmd := jsonCommand("move LIST_ID", "Move/reorder a list to a new position", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.MoveList(ctx, args[0], position)
		}
	})
	cmd.Flags().IntVar(&position, "position", 0, "New position")
	_ = cmd.MarkFlagRequired("position")
	return cmd
}

func jsonCommand(use string, short string, args cobra.PositionalArgs, root *rootOptions, build func(*cobra.Command, []string) func(context.Context, *api.Client) (json.RawMessage, error)) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  args,
		RunE: func(cmd *cobra.Command, argv []string) error {
			return runJSON(cmd, root, build(cmd, argv))
		},
	}
}

func extractMembersSection(markdown string) string {
	return extractMarkdownSection(markdown, "## Members", "No members section found.")
}

func extractMarkdownSection(markdown, heading, emptyMessage string) string {
	lines := strings.Split(markdown, "\n")
	var out []string
	inSection := false
	for _, line := range lines {
		if strings.HasPrefix(line, heading) {
			inSection = true
			out = append(out, line)
			continue
		}
		if inSection && strings.HasPrefix(line, "## ") {
			break
		}
		if inSection {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return emptyMessage
	}
	return strings.TrimSpace(strings.Join(out, "\n")) + "\n"
}

func formatLabelsMarkdown(labels []api.Label) string {
	var out strings.Builder
	out.WriteString("## Labels\n\n")
	if len(labels) == 0 {
		out.WriteString("_No labels defined._\n")
		return out.String()
	}
	for _, label := range labels {
		name := label.Name
		if name == "" {
			name = label.ID
		}
		fmt.Fprintf(&out, "- %s (`%s`)\n", name, label.ID)
	}
	return out.String()
}

func expandPublishedText(value string) string {
	return strings.ReplaceAll(value, `\n`, "\n")
}

func expandPublishedTextSlice(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = expandPublishedText(value)
	}
	return out
}

func buildCardPatch(title, description, dueDate, assigneeID string) api.CardPatch {
	patch := api.CardPatch{}
	if title != "" {
		patch.Title = &title
	}
	if description != "" {
		patch.Description = &description
	}
	if dueDate != "" {
		patch.DueDate = &dueDate
	}
	if assigneeID != "" {
		patch.AssigneeID = &assigneeID
		patch.AssigneeSet = true
	}
	return patch
}

func hasCardPatchUpdates(patch api.CardPatch) bool {
	return patch.Title != nil || patch.Description != nil || patch.DueDate != nil || patch.AssigneeSet
}

func labelIDsFromLabels(labels []api.Label) []string {
	labelIDs := make([]string, 0, len(labels))
	for _, label := range labels {
		labelIDs = append(labelIDs, label.ID)
	}
	return labelIDs
}

func buildTodoPatch(title, dueDate string, completed bool, completedSet bool, assignees []string) (api.TodoPatch, error) {
	patch := api.TodoPatch{}
	if title != "" {
		patch.Title = &title
	}
	if dueDate != "" {
		patch.DueDate = &dueDate
	}
	if completedSet {
		patch.IsCompleted = &completed
	}
	if len(assignees) > 0 {
		patch.AssigneeIDs = assignees
		patch.AssigneeSet = true
	}
	if patch.Title == nil && patch.DueDate == nil && patch.IsCompleted == nil && !patch.AssigneeSet {
		return patch, fmt.Errorf("at least one update flag is required")
	}
	return patch, nil
}

func buildLinkPatch(linkURL, displayText string) (api.LinkPatch, error) {
	patch := api.LinkPatch{}
	if linkURL != "" {
		patch.URL = &linkURL
	}
	if displayText != "" {
		patch.DisplayText = &displayText
	}
	if patch.URL == nil && patch.DisplayText == nil {
		return patch, fmt.Errorf("at least one update flag is required")
	}
	return patch, nil
}

func contentFromFlags(content, contentFile string, stdin io.Reader) (string, error) {
	if content != "" && contentFile != "" {
		return "", fmt.Errorf("--content and --content-file are mutually exclusive")
	}
	if content != "" {
		return expandPublishedText(content), nil
	}
	if contentFile == "" {
		return "", fmt.Errorf("--content or --content-file is required")
	}
	var data []byte
	var err error
	if contentFile == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(contentFile)
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}
