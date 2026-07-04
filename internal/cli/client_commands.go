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
	return &cobra.Command{
		Use:   "list",
		Short: "List all accessible boards",
		RunE: func(cmd *cobra.Command, args []string) error {
			if root.format == "md" {
				return runMarkdown(cmd, root, func(ctx context.Context, client *api.Client) (string, error) {
					return client.ListBoardsMarkdown(ctx)
				})
			}
			return runJSON(cmd, root, func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
				return client.ListBoards(ctx)
			})
		},
	}
}

func boardLabels(root *rootOptions) *cobra.Command {
	return jsonCommand("labels BOARD_ID", "Get all labels defined on a board", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
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
			if root.format == "md" {
				return runMarkdown(cmd, root, func(ctx context.Context, client *api.Client) (string, error) {
					return client.GetBoardActivityMarkdown(ctx, args[0], opts)
				})
			}
			return runJSON(cmd, root, func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
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
			if root.format == "md" {
				return runMarkdown(cmd, root, func(ctx context.Context, client *api.Client) (string, error) {
					markdown, err := client.GetBoardMarkdown(ctx, args[0], false)
					if err != nil {
						return "", err
					}
					return extractMembersSection(markdown), nil
				})
			}
			return runJSON(cmd, root, func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
				raw, err := client.GetBoard(ctx, args[0], false)
				if err != nil {
					return nil, err
				}
				var board map[string]json.RawMessage
				if err := json.Unmarshal(raw, &board); err != nil {
					return nil, err
				}
				if members, ok := board["members"]; ok {
					return members, nil
				}
				return json.RawMessage(`[]`), nil
			})
		},
	}
}

func boardUpdate(root *rootOptions) *cobra.Command {
	var name string
	cmd := jsonCommand("update BOARD_ID", "Update a board's name", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.UpdateBoard(ctx, args[0], name)
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
	cmd := jsonCommand("search BOARD_ID QUERY", "Search cards on a board by title", cobra.ExactArgs(2), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
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
			return client.CreateCard(ctx, boardID, listID, title, description)
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
	cmd := jsonCommand("update CARD_ID", "Update a card's fields", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			patch, err := buildCardPatch(title, description, dueDate, assigneeID, labelIDs)
			if err != nil {
				return nil, err
			}
			return client.UpdateCard(ctx, args[0], patch)
		}
	})
	cmd.Flags().StringVar(&title, "title", "", "New title")
	cmd.Flags().StringVar(&description, "description", "", "New description")
	cmd.Flags().StringVar(&dueDate, "due", "", "Due date")
	cmd.Flags().StringVar(&assigneeID, "assignee", "", "Assignee user ID")
	cmd.Flags().StringArrayVar(&labelIDs, "label", nil, "Label IDs")
	cmd.Flags().StringArrayVar(&labelIDs, "label-ids", nil, "Label IDs")
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
	cmd := jsonCommand("activity CARD_ID", "Get recent activity on a card", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
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
				return client.AddComment(ctx, args[0], args[1])
			}
		}),
		&cobra.Command{
			Use:   "delete CARD_ID COMMENT_ID",
			Short: "Delete a comment",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
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
			return client.CreateChecklist(ctx, args[0], title)
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
			return client.AddTodo(ctx, args[0], checklistID, title)
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
			return client.AddTodos(ctx, args[0], title, args[1:])
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
			patch, err := buildTodoPatch(title, dueDate, completed, completedSet, assignees)
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
				return client.ExtractChecklistToCards(ctx, args[0], checklistID, targetListID, prefix)
			}
			return client.ExtractTodosToCards(ctx, args[0], targetListID, prefix)
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
	return jsonCommand("list CARD_ID", "List all attachments on a card", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
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
		jsonCommand("list CARD_ID", "List all links on a card", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
			return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
				return client.ListCardLinks(ctx, args[0])
			}
		}),
		linkUpdate(root),
		linkDelete(root),
	)
	return group
}

func linkAdd(root *rootOptions) *cobra.Command {
	var displayText string
	cmd := jsonCommand("add CARD_ID URL", "Add a URL link to a card", cobra.ExactArgs(2), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			return client.AddCardLink(ctx, args[0], args[1], displayText)
		}
	})
	cmd.Flags().StringVar(&displayText, "text", "", "Display text for the link")
	return cmd
}

func linkUpdate(root *rootOptions) *cobra.Command {
	var linkURL, displayText string
	cmd := jsonCommand("update CARD_ID LINK_ID", "Update a link", cobra.ExactArgs(2), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
		return func(ctx context.Context, client *api.Client) (json.RawMessage, error) {
			patch, err := buildLinkPatch(linkURL, displayText)
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
	cmd := jsonCommand("search QUERY", "Search cards across all accessible boards", cobra.ExactArgs(1), root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
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
	cmd := jsonCommand("activity", "Get cross-board activity feed", cobra.NoArgs, root, func(_ *cobra.Command, args []string) func(context.Context, *api.Client) (json.RawMessage, error) {
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
			return client.CreateList(ctx, args[0], name)
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
	lines := strings.Split(markdown, "\n")
	var out []string
	inMembers := false
	for _, line := range lines {
		if strings.HasPrefix(line, "## Members") {
			inMembers = true
			out = append(out, line)
			continue
		}
		if inMembers && strings.HasPrefix(line, "## ") {
			break
		}
		if inMembers {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return "No members section found."
	}
	return strings.TrimSpace(strings.Join(out, "\n")) + "\n"
}

func buildCardPatch(title, description, dueDate, assigneeID string, labelIDs []string) (api.CardPatch, error) {
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
	if len(labelIDs) > 0 {
		patch.LabelIDs = labelIDs
		patch.LabelSet = true
	}
	if patch.Title == nil && patch.Description == nil && patch.DueDate == nil && !patch.AssigneeSet && !patch.LabelSet {
		return patch, fmt.Errorf("at least one update flag is required")
	}
	return patch, nil
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
		return content, nil
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
