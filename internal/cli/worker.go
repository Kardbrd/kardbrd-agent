package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/worker"
	"github.com/spf13/cobra"
)

type workerFlags struct {
	boardID         string
	workerID        string
	pollInterval    time.Duration
	lease           time.Duration
	timeout         time.Duration
	maxConcurrent   int
	artifactDir     string
	outputLimit     int
	packetLimit     int
	noticeTimeout   time.Duration
	runner          string
	runnerArgs      []string
	notice          string
	noticeArgs      []string
	observer        string
	observerArgs    []string
	observerLimit   int
	observerMax     int
	observerTimeout time.Duration
	registryCardID  string
}

func NewWorkerCommand(root *rootOptions) *cobra.Command {
	flags := &workerFlags{}
	group := &cobra.Command{
		Use:   "worker",
		Short: "Run opt-in durable personal-operations work from a Kardbrd board",
		Long:  "Personal worker commands use only versioned card metadata as durable state. They do not start the coding agent, create worktrees, or configure provider connections.",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if !formatFlagChanged(cmd) {
				return nil
			}
			if !isKnownFormat(root.format) {
				return fmt.Errorf("--format must be one of %s", strings.Join([]string{formatTSV, formatJSON, formatMD}, ", "))
			}
			return fmt.Errorf("--format %q is not supported by %s; worker commands emit their own JSON", root.format, cmd.CommandPath())
		},
	}
	addWorkerFlags(group, flags)
	group.AddCommand(workerCheck(root, flags), workerRunOnce(root, flags), workerServe(root, flags), workerEnroll(root, flags), workerWake(root, flags), workerDecide(root, flags), workerRegistry(root, flags), workerIngest(root, flags))
	return group
}

func addWorkerFlags(cmd *cobra.Command, flags *workerFlags) {
	cmd.PersistentFlags().StringVar(&flags.boardID, "board-id", "", "Explicit Kardbrd board ID used as the durable queue")
	cmd.PersistentFlags().StringVar(&flags.workerID, "worker-id", "", "Stable operator-selected worker identity for execution")
	cmd.PersistentFlags().DurationVar(&flags.pollInterval, "poll-interval", 30*time.Second, "Interval between board-wide passes in serve mode")
	cmd.PersistentFlags().DurationVar(&flags.lease, "lease", 2*time.Minute, "Durable claim lease duration")
	cmd.PersistentFlags().DurationVar(&flags.timeout, "timeout", 5*time.Minute, "Maximum runner or observer duration")
	cmd.PersistentFlags().IntVar(&flags.maxConcurrent, "max-concurrent", 1, "Maximum concurrent delegated runs")
	cmd.PersistentFlags().StringVar(&flags.artifactDir, "artifact-dir", ".kardbrd-worker-artifacts", "Local directory for per-run private artifacts")
	cmd.PersistentFlags().IntVar(&flags.outputLimit, "output-limit", 64*1024, "Maximum stdout or stderr bytes from a runner")
	cmd.PersistentFlags().IntVar(&flags.packetLimit, "packet-limit", 64*1024, "Maximum JSON work packet bytes passed to a runner")
	cmd.PersistentFlags().StringVar(&flags.runner, "runner", "", "Trusted runner executable path; never derived from card content")
	cmd.PersistentFlags().StringArrayVar(&flags.runnerArgs, "runner-arg", nil, "Trusted runner argument (repeatable)")
	cmd.PersistentFlags().StringVar(&flags.notice, "notice-command", "", "Optional trusted notification executable")
	cmd.PersistentFlags().StringArrayVar(&flags.noticeArgs, "notice-arg", nil, "Optional notification executable argument (repeatable)")
	cmd.PersistentFlags().DurationVar(&flags.noticeTimeout, "notice-timeout", 30*time.Second, "Maximum notification adapter duration")
	cmd.PersistentFlags().StringVar(&flags.observer, "observer", "", "Trusted read-only suggestion observer executable")
	cmd.PersistentFlags().StringArrayVar(&flags.observerArgs, "observer-arg", nil, "Trusted observer argument (repeatable)")
	cmd.PersistentFlags().IntVar(&flags.observerLimit, "observer-output-limit", 64*1024, "Maximum observer stdout or stderr bytes")
	cmd.PersistentFlags().IntVar(&flags.observerMax, "observer-max-events", 100, "Maximum source events accepted from one observer pass")
	cmd.PersistentFlags().DurationVar(&flags.observerTimeout, "observer-timeout", 2*time.Minute, "Maximum observer duration")
	cmd.PersistentFlags().StringVar(&flags.registryCardID, "suggestion-registry-card", "", "Paused operator-selected card that durably reserves suggestion source IDs")
}

func workerCheck(root *rootOptions, flags *workerFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Read board worker state and report due delegated work without running anything",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, err := newWorkerService(cmd.Context(), root, flags, false)
			if err != nil {
				return err
			}
			report, err := service.Check(cmd.Context())
			if err != nil {
				return err
			}
			return outputWorkerJSON(cmd, report)
		},
	}
}

func workerRunOnce(root *rootOptions, flags *workerFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "run-once",
		Short: "Claim and process one board-wide pass of due delegated work",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, err := newWorkerService(cmd.Context(), root, flags, true)
			if err != nil {
				return err
			}
			report, err := service.RunOnce(cmd.Context())
			if err != nil {
				return err
			}
			return outputWorkerJSON(cmd, report)
		},
	}
}

func workerServe(root *rootOptions, flags *workerFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run one durable worker loop until interrupted",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, err := newWorkerService(cmd.Context(), root, flags, true)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return service.Serve(ctx)
		},
	}
}

func workerEnroll(root *rootOptions, flags *workerFlags) *cobra.Command {
	var goal, criteria, authorization, actionID, actionIntent string
	var sources []string
	cmd := &cobra.Command{
		Use:   "enroll CARD_ID",
		Short: "Explicitly delegate a card to the personal worker",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			service, err := newWorkerService(cmd.Context(), root, flags, false)
			if err != nil {
				return err
			}
			auth := json.RawMessage(authorization)
			if !jsonObjectValue(auth) {
				return errors.New("--authorization must be a nonempty JSON object")
			}
			var action *worker.ActionIntent
			if actionID != "" || actionIntent != "" {
				if actionID == "" || !jsonObjectValue(json.RawMessage(actionIntent)) {
					return errors.New("--action-id and --action-intent JSON object must be supplied together")
				}
				action = &worker.ActionIntent{ID: actionID, Intent: json.RawMessage(actionIntent)}
			}
			refs := make([]worker.SourceRef, 0, len(sources))
			for _, source := range sources {
				id, reference, _ := strings.Cut(source, "=")
				if id == "" {
					return errors.New("--source must be SOURCE_ID or SOURCE_ID=REFERENCE")
				}
				refs = append(refs, worker.SourceRef{ID: id, Reference: reference})
			}
			return service.Enroll(cmd.Context(), args[0], worker.Enrollment{Goal: goal, CompletionCriteria: criteria, Authorization: auth, Sources: refs, Action: action})
		},
	}
	cmd.Flags().StringVar(&goal, "goal", "", "Exact delegated goal")
	cmd.Flags().StringVar(&criteria, "completion-criteria", "", "Exact completion criteria")
	cmd.Flags().StringVar(&authorization, "authorization", "", "Explicit JSON object authorization")
	cmd.Flags().StringArrayVar(&sources, "source", nil, "Stable SOURCE_ID or SOURCE_ID=REFERENCE (repeatable)")
	cmd.Flags().StringVar(&actionID, "action-id", "", "Stable predeclared outward action ID")
	cmd.Flags().StringVar(&actionIntent, "action-intent", "", "Predeclared outward action intent JSON object")
	_ = cmd.MarkFlagRequired("goal")
	_ = cmd.MarkFlagRequired("completion-criteria")
	_ = cmd.MarkFlagRequired("authorization")
	return cmd
}

func workerWake(root *rootOptions, flags *workerFlags) *cobra.Command {
	var eventID string
	cmd := &cobra.Command{
		Use:   "wake CARD_ID",
		Short: "Record a stable external event receipt and make its card ready",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			service, err := newWorkerService(cmd.Context(), root, flags, false)
			if err != nil {
				return err
			}
			changed, err := service.Wake(cmd.Context(), args[0], eventID)
			if err != nil {
				return err
			}
			return outputWorkerJSON(cmd, map[string]bool{"changed": changed})
		},
	}
	cmd.Flags().StringVar(&eventID, "event-id", "", "Stable external event ID")
	_ = cmd.MarkFlagRequired("event-id")
	return cmd
}

func workerDecide(root *rootOptions, flags *workerFlags) *cobra.Command {
	var decisionID, value string
	cmd := &cobra.Command{
		Use:   "decide CARD_ID",
		Short: "Record an explicit user decision and resume only that waiting card",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			service, err := newWorkerService(cmd.Context(), root, flags, false)
			if err != nil {
				return err
			}
			changed, err := service.Decide(cmd.Context(), args[0], decisionID, json.RawMessage(value))
			if err != nil {
				return err
			}
			return outputWorkerJSON(cmd, map[string]bool{"changed": changed})
		},
	}
	cmd.Flags().StringVar(&decisionID, "decision-id", "", "Stable decision receipt ID")
	cmd.Flags().StringVar(&value, "value", "", "JSON decision value")
	_ = cmd.MarkFlagRequired("decision-id")
	_ = cmd.MarkFlagRequired("value")
	return cmd
}

func workerRegistry(root *rootOptions, flags *workerFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "registry CARD_ID",
		Short: "Initialize a paused card as the durable suggestion source registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			service, err := newWorkerService(cmd.Context(), root, flags, false)
			if err != nil {
				return err
			}
			return service.InitSuggestionRegistry(cmd.Context(), args[0])
		},
	}
}

func workerIngest(root *rootOptions, flags *workerFlags) *cobra.Command {
	var listID string
	cmd := &cobra.Command{
		Use:   "ingest",
		Short: "Ingest read-only observer suggestions as non-delegated proposals",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flags.observer == "" {
				return errors.New("--observer is required")
			}
			if flags.registryCardID == "" {
				return errors.New("--suggestion-registry-card is required")
			}
			if flags.observerLimit <= 0 || flags.observerTimeout <= 0 || flags.observerMax <= 0 {
				return errors.New("observer timeout, output limit, and maximum event count must be positive")
			}
			service, err := newWorkerService(cmd.Context(), root, flags, false)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), flags.observerTimeout)
			defer cancel()
			events, err := (worker.SubprocessObserver{Argv: append([]string{flags.observer}, flags.observerArgs...), OutputLimit: flags.observerLimit, MaxEvents: flags.observerMax}).Observe(ctx)
			if err != nil {
				return err
			}
			report, err := service.IngestSuggestions(cmd.Context(), flags.registryCardID, listID, events)
			if err != nil {
				return err
			}
			return outputWorkerJSON(cmd, report)
		},
	}
	cmd.Flags().StringVar(&listID, "list-id", "", "List for newly created suggestion cards")
	_ = cmd.MarkFlagRequired("list-id")
	return cmd
}

func newWorkerService(_ context.Context, root *rootOptions, flags *workerFlags, execution bool) (worker.Service, error) {
	if flags.boardID == "" {
		return worker.Service{}, errors.New("--board-id is required")
	}
	client, err := newClient(root)
	if err != nil {
		return worker.Service{}, err
	}
	// A worker must never replay a metadata claim, action journal, comment, or
	// observer-triggered creation after a transport ambiguity.
	client.SetNoRetry(true)
	config := worker.Config{
		BoardID:       flags.boardID,
		WorkerID:      flags.workerID,
		PollInterval:  flags.pollInterval,
		LeaseDuration: flags.lease,
		RunTimeout:    flags.timeout,
		MaxConcurrent: flags.maxConcurrent,
		ArtifactDir:   flags.artifactDir,
		OutputLimit:   flags.outputLimit,
		PacketLimit:   flags.packetLimit,
		NoticeTimeout: flags.noticeTimeout,
	}
	if execution {
		if flags.runner == "" {
			return worker.Service{}, errors.New("--runner is required for worker execution")
		}
		config.Runner = worker.SubprocessRunner{Argv: append([]string{flags.runner}, flags.runnerArgs...), ArtifactDir: flags.artifactDir, OutputLimit: flags.outputLimit, PacketLimit: flags.packetLimit}
		if flags.notice != "" {
			config.Notifier = worker.SubprocessNotifier{Argv: append([]string{flags.notice}, flags.noticeArgs...), OutputLimit: flags.outputLimit}
		}
	}
	return worker.Service{Store: worker.Store{Client: client}, Config: config}, nil
}

func outputWorkerJSON(cmd *cobra.Command, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
	return err
}

func jsonObjectValue(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &value) == nil && value != nil
}
