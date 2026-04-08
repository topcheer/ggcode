package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/topcheer/ggcode/internal/harness"
)

func newHarnessCmd() *cobra.Command {
	var initGoal string
	var initForce bool
	var runAllQueued bool
	var runRetryFailed bool
	var runResumeInterrupted bool
	var queueDependsOn []string
	var queueContext string
	var runContext string
	var reviewNote string
	var promoteNote string
	var promoteAllApproved bool
	var releaseBatchID string
	var releaseNote string
	var releaseOwner string
	var releaseContext string
	var releaseEnvironment string
	var releaseGroupBy string
	var releaseRolloutID string
	var releaseWaveOrder int
	var inboxOwner string
	var inboxNote string
	var monitorWatch bool
	var monitorInterval time.Duration
	var monitorRecentEvents int
	var monitorFocusTasks int

	cmd := &cobra.Command{
		Use:   "harness",
		Short: "Manage harness-engineering workflows",
	}

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize harness scaffolding in the current project",
		RunE: func(cmd *cobra.Command, args []string) error {
			workDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}
			result, err := harness.Init(workDir, harness.InitOptions{
				Goal:  initGoal,
				Force: initForce,
			})
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), formatInitResult(result))
			return nil
		},
	}
	initCmd.Flags().StringVar(&initGoal, "goal", "", "project goal to embed in the harness scaffold")
	initCmd.Flags().BoolVar(&initForce, "force", false, "overwrite existing harness scaffold files")

	checkCmd := &cobra.Command{
		Use:   "check",
		Short: "Run harness structural checks and validation commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, cfg, err := loadHarnessProject()
			if err != nil {
				return err
			}
			report, err := harness.CheckProject(context.Background(), project, cfg, harness.CheckOptions{RunCommands: true})
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatCheckReport(report)+"\n")
			if report.Passed {
				return nil
			}
			return fmt.Errorf("harness checks failed")
		},
	}

	queueCmd := &cobra.Command{
		Use:   "queue <goal>",
		Short: "Queue a harness task for later execution",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, cfg, err := loadHarnessProject()
			if err != nil {
				return err
			}
			contextCfg, err := harness.ResolveContext(cfg, queueContext)
			if err != nil {
				return err
			}
			opts := harness.QueueOptions{DependsOn: queueDependsOn}
			if contextCfg != nil {
				opts.ContextName = contextCfg.Name
				opts.ContextPath = contextCfg.Path
			}
			task, err := harness.EnqueueTask(project, strings.Join(args, " "), "cli", opts)
			if err != nil {
				return err
			}
			var b strings.Builder
			b.WriteString(fmt.Sprintf("Queued harness task %s.\n- goal: %s\n", task.ID, task.Goal))
			if len(task.DependsOn) > 0 {
				b.WriteString("- depends_on: ")
				b.WriteString(strings.Join(task.DependsOn, ", "))
				b.WriteString("\n")
			}
			if task.ContextName != "" || task.ContextPath != "" {
				b.WriteString("- context: ")
				b.WriteString(harnessFirstNonEmpty(task.ContextName, task.ContextPath))
				b.WriteString("\n")
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), b.String())
			return nil
		},
	}
	queueCmd.Flags().StringArrayVar(&queueDependsOn, "depends-on", nil, "task ID prerequisite; can be repeated")
	queueCmd.Flags().StringVar(&queueContext, "context", "", "context name or path for this task")

	runCmd := &cobra.Command{
		Use:   "run [goal]",
		Short: "Execute a tracked harness task or queued backlog",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			project, cfg, err := loadHarnessProject()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				queueSummary, err := harness.RunQueuedTasks(context.Background(), project, cfg, harness.BinaryRunner{}, harness.QueueRunOptions{
					All:               runAllQueued,
					RetryFailed:       runRetryFailed,
					ResumeInterrupted: runResumeInterrupted,
				})
				if err != nil {
					return err
				}
				_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatQueueSummary(queueSummary)+"\n")
				for _, item := range queueSummary.Executed {
					if item != nil && item.Task != nil && item.Task.Status != harness.TaskCompleted {
						return fmt.Errorf("harness queue run failed")
					}
				}
				return nil
			}
			contextCfg, err := harness.ResolveContext(cfg, runContext)
			if err != nil {
				return err
			}
			runOpts := harness.RunTaskOptions{}
			if contextCfg != nil {
				runOpts.ContextName = contextCfg.Name
				runOpts.ContextPath = contextCfg.Path
			}
			summary, err := harness.RunTaskWithOptions(context.Background(), project, cfg, strings.Join(args, " "), harness.BinaryRunner{}, runOpts)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatRunSummary(summary)+"\n")
			if summary.Task != nil && summary.Task.Status != harness.TaskCompleted {
				return fmt.Errorf("harness run failed")
			}
			return nil
		},
	}
	runCmd.Flags().BoolVar(&runAllQueued, "all-queued", false, "run every queued task sequentially when no goal is provided")
	runCmd.Flags().BoolVar(&runRetryFailed, "retry-failed", false, "also rerun failed queued tasks that are still under the harness max_attempts limit")
	runCmd.Flags().BoolVar(&runResumeInterrupted, "resume-interrupted", false, "also rerun tasks left in running state after an interrupted harness execution")
	runCmd.Flags().StringVar(&runContext, "context", "", "context name or path for a directly-run task")

	rerunCmd := &cobra.Command{
		Use:   "rerun <task-id>",
		Short: "Rerun an individual failed harness task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, cfg, err := loadHarnessProject()
			if err != nil {
				return err
			}
			summary, err := harness.RerunTask(context.Background(), project, cfg, args[0], harness.BinaryRunner{})
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatRunSummary(summary)+"\n")
			if summary.Task != nil && summary.Task.Status != harness.TaskCompleted {
				return fmt.Errorf("harness rerun failed")
			}
			return nil
		},
	}

	tasksCmd := &cobra.Command{
		Use:   "tasks",
		Short: "List harness tasks and their execution state",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _, err := loadHarnessProject()
			if err != nil {
				return err
			}
			tasks, err := harness.ListTasks(project)
			if err != nil {
				return err
			}
			if len(tasks) == 0 {
				_, _ = writeCLIText(cmd.OutOrStdout(), "No harness tasks recorded.\n")
				return nil
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatTaskList(tasks)+"\n")
			return nil
		},
	}

	monitorCmd := &cobra.Command{
		Use:   "monitor",
		Short: "Show persisted harness activity from the monitor snapshot",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _, err := loadHarnessProject()
			if err != nil {
				return err
			}
			if monitorInterval <= 0 {
				return fmt.Errorf("provide a positive --interval")
			}
			return runHarnessMonitor(cmd.OutOrStdout(), project, harness.MonitorOptions{
				RecentEvents: monitorRecentEvents,
				FocusTasks:   monitorFocusTasks,
			}, monitorWatch, monitorInterval)
		},
	}
	monitorCmd.Flags().BoolVar(&monitorWatch, "watch", false, "refresh the monitor view on an interval until interrupted")
	monitorCmd.Flags().DurationVar(&monitorInterval, "interval", 2*time.Second, "refresh interval for --watch")
	monitorCmd.Flags().IntVar(&monitorRecentEvents, "events", 8, "how many recent events to show")
	monitorCmd.Flags().IntVar(&monitorFocusTasks, "focus-tasks", 6, "how many focus tasks to show")

	contextsCmd := &cobra.Command{
		Use:   "contexts",
		Short: "Summarize harness state per bounded context",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, cfg, err := loadHarnessProject()
			if err != nil {
				return err
			}
			report, err := harness.BuildContextReport(project, cfg)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatContextReport(report)+"\n")
			return nil
		},
	}

	inboxCmd := &cobra.Command{
		Use:   "inbox",
		Short: "Show owner-centric actionable harness work",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, cfg, err := loadHarnessProject()
			if err != nil {
				return err
			}
			inbox, err := harness.BuildOwnerInbox(project, cfg)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatOwnerInbox(inbox)+"\n")
			return nil
		},
	}
	inboxPromoteCmd := &cobra.Command{
		Use:   "promote",
		Short: "Promote all promotion-ready tasks for one owner",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, cfg, err := loadHarnessProject()
			if err != nil {
				return err
			}
			if strings.TrimSpace(inboxOwner) == "" {
				return fmt.Errorf("provide --owner")
			}
			tasks, err := harness.PromoteApprovedTasksForOwner(context.Background(), project, cfg, inboxOwner, inboxNote)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), fmt.Sprintf("Promoted %d harness task(s) for owner %s.\n", len(tasks), inboxOwner))
			return nil
		},
	}
	inboxRetryCmd := &cobra.Command{
		Use:   "retry",
		Short: "Retry all retryable tasks for one owner",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, cfg, err := loadHarnessProject()
			if err != nil {
				return err
			}
			if strings.TrimSpace(inboxOwner) == "" {
				return fmt.Errorf("provide --owner")
			}
			summary, err := harness.RetryFailedTasksForOwner(context.Background(), project, cfg, inboxOwner, harness.BinaryRunner{})
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatQueueSummary(summary)+"\n")
			for _, item := range summary.Executed {
				if item != nil && item.Task != nil && item.Task.Status != harness.TaskCompleted {
					return fmt.Errorf("harness owner retry failed")
				}
			}
			return nil
		},
	}
	inboxPromoteCmd.Flags().StringVar(&inboxOwner, "owner", "", "context owner to target; use 'unowned' for tasks without an owner")
	inboxPromoteCmd.Flags().StringVar(&inboxNote, "note", "", "optional promotion note to persist with the batch action")
	inboxRetryCmd.Flags().StringVar(&inboxOwner, "owner", "", "context owner to target; use 'unowned' for tasks without an owner")
	inboxCmd.AddCommand(inboxPromoteCmd, inboxRetryCmd)

	reviewCmd := &cobra.Command{
		Use:   "review",
		Short: "List or resolve harness tasks waiting for review",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _, err := loadHarnessProject()
			if err != nil {
				return err
			}
			tasks, err := harness.ListReviewableTasks(project)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatReviewList(tasks)+"\n")
			return nil
		},
	}

	reviewApproveCmd := &cobra.Command{
		Use:   "approve <task-id>",
		Short: "Approve a completed harness task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _, err := loadHarnessProject()
			if err != nil {
				return err
			}
			task, err := harness.ApproveTaskReview(project, args[0], reviewNote)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), fmt.Sprintf("Approved harness task %s.\n", task.ID))
			return nil
		},
	}

	reviewRejectCmd := &cobra.Command{
		Use:   "reject <task-id>",
		Short: "Reject a completed harness task back into the retry flow",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _, err := loadHarnessProject()
			if err != nil {
				return err
			}
			task, err := harness.RejectTaskReview(project, args[0], reviewNote)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), fmt.Sprintf("Rejected harness task %s.\n", task.ID))
			return nil
		},
	}
	reviewApproveCmd.Flags().StringVar(&reviewNote, "note", "", "optional review note to persist with the decision")
	reviewRejectCmd.Flags().StringVar(&reviewNote, "note", "", "optional review note to persist with the decision")
	reviewCmd.AddCommand(reviewApproveCmd, reviewRejectCmd)

	promoteCmd := &cobra.Command{
		Use:   "promote",
		Short: "List or apply harness tasks ready for promotion",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _, err := loadHarnessProject()
			if err != nil {
				return err
			}
			tasks, err := harness.ListPromotableTasks(project)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatPromotionList(tasks)+"\n")
			return nil
		},
	}

	promoteApplyCmd := &cobra.Command{
		Use:   "apply [task-id]",
		Short: "Promote one approved task or every approved task",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _, err := loadHarnessProject()
			if err != nil {
				return err
			}
			if promoteAllApproved {
				tasks, err := harness.PromoteApprovedTasks(context.Background(), project, promoteNote)
				if err != nil {
					return err
				}
				_, _ = writeCLIText(cmd.OutOrStdout(), fmt.Sprintf("Promoted %d harness task(s).\n", len(tasks)))
				return nil
			}
			if len(args) != 1 {
				return fmt.Errorf("provide a task ID or use --all-approved")
			}
			task, err := harness.PromoteTask(context.Background(), project, args[0], promoteNote)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), fmt.Sprintf("Promoted harness task %s.\n", task.ID))
			return nil
		},
	}
	promoteApplyCmd.Flags().StringVar(&promoteNote, "note", "", "optional promotion note to persist with the decision")
	promoteApplyCmd.Flags().BoolVar(&promoteAllApproved, "all-approved", false, "promote every approved task in dependency order")
	promoteCmd.AddCommand(promoteApplyCmd)

	releaseCmd := &cobra.Command{
		Use:   "release",
		Short: "List or apply promoted harness tasks into a release batch",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, cfg, err := loadHarnessProject()
			if err != nil {
				return err
			}
			if strings.TrimSpace(releaseGroupBy) != "" {
				waves, err := harness.BuildReleaseWavePlan(project, cfg, harness.ReleasePlanOptions{
					Owner:       releaseOwner,
					Context:     releaseContext,
					Environment: releaseEnvironment,
				}, releaseGroupBy)
				if err != nil {
					return err
				}
				_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatReleaseWavePlan(waves)+"\n")
				return nil
			}
			plan, err := harness.BuildReleasePlanWithOptions(project, cfg, harness.ReleasePlanOptions{
				Owner:       releaseOwner,
				Context:     releaseContext,
				Environment: releaseEnvironment,
			})
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatReleasePlan(plan)+"\n")
			return nil
		},
	}

	releaseApplyCmd := &cobra.Command{
		Use:   "apply",
		Short: "Record a release batch for every promoted harness task waiting to ship",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, cfg, err := loadHarnessProject()
			if err != nil {
				return err
			}
			if strings.TrimSpace(releaseGroupBy) != "" {
				waves, err := harness.BuildReleaseWavePlan(project, cfg, harness.ReleasePlanOptions{
					Owner:       releaseOwner,
					Context:     releaseContext,
					Environment: releaseEnvironment,
				}, releaseGroupBy)
				if err != nil {
					return err
				}
				waves, err = harness.ApplyReleaseWavePlan(project, waves, releaseNote, releaseBatchID)
				if err != nil {
					return err
				}
				_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatReleaseWavePlan(waves)+"\n")
				return nil
			}
			plan, err := harness.BuildReleasePlanWithOptions(project, cfg, harness.ReleasePlanOptions{
				Owner:       releaseOwner,
				Context:     releaseContext,
				Environment: releaseEnvironment,
			})
			if err != nil {
				return err
			}
			if strings.TrimSpace(releaseBatchID) != "" {
				plan.BatchID = strings.TrimSpace(releaseBatchID)
			}
			plan, err = harness.ApplyReleasePlan(project, plan, releaseNote)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatReleasePlan(plan)+"\n")
			return nil
		},
	}
	releaseCmd.Flags().StringVar(&releaseOwner, "owner", "", "restrict release planning to one owner; use 'unowned' for tasks without an owner")
	releaseCmd.Flags().StringVar(&releaseContext, "context", "", "restrict release planning to one context name or path")
	releaseCmd.Flags().StringVar(&releaseEnvironment, "environment", "", "tag this release plan with an environment such as staging or prod")
	releaseCmd.Flags().StringVar(&releaseGroupBy, "group-by", "", "split the release plan into waves by 'owner' or 'context'")
	releaseApplyCmd.Flags().StringVar(&releaseOwner, "owner", "", "restrict release application to one owner; use 'unowned' for tasks without an owner")
	releaseApplyCmd.Flags().StringVar(&releaseContext, "context", "", "restrict release application to one context name or path")
	releaseApplyCmd.Flags().StringVar(&releaseEnvironment, "environment", "", "tag this release apply with an environment such as staging or prod")
	releaseApplyCmd.Flags().StringVar(&releaseGroupBy, "group-by", "", "split release application into waves by 'owner' or 'context'")
	releaseApplyCmd.Flags().StringVar(&releaseBatchID, "batch-id", "", "optional release batch ID override")
	releaseApplyCmd.Flags().StringVar(&releaseNote, "note", "", "optional release note to persist on released tasks")
	releaseRolloutsCmd := &cobra.Command{
		Use:   "rollouts",
		Short: "List persisted grouped release rollouts and their current wave state",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _, err := loadHarnessProject()
			if err != nil {
				return err
			}
			rollouts, err := harness.ListReleaseWaveRollouts(project)
			if err != nil {
				return err
			}
			rollouts = harness.FilterReleaseWaveRolloutsByEnvironment(rollouts, releaseEnvironment)
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatReleaseWaveRollouts(rollouts)+"\n")
			return nil
		},
	}
	releaseRolloutsCmd.Flags().StringVar(&releaseEnvironment, "environment", "", "filter persisted grouped release rollouts by environment")
	releaseAdvanceCmd := &cobra.Command{
		Use:   "advance <rollout-id>",
		Short: "Advance a grouped release rollout to the next wave",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _, err := loadHarnessProject()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				releaseRolloutID = args[0]
			}
			rollout, err := harness.AdvanceReleaseWaveRollout(project, releaseRolloutID)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatReleaseWavePlan(rollout)+"\n")
			return nil
		},
	}
	releaseAdvanceCmd.Flags().StringVar(&releaseRolloutID, "rollout", "", "rollout ID to advance")
	releasePauseCmd := &cobra.Command{
		Use:   "pause <rollout-id>",
		Short: "Pause the active wave in a grouped release rollout",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _, err := loadHarnessProject()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				releaseRolloutID = args[0]
			}
			rollout, err := harness.PauseReleaseWaveRollout(project, releaseRolloutID, releaseNote)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatReleaseWavePlan(rollout)+"\n")
			return nil
		},
	}
	releasePauseCmd.Flags().StringVar(&releaseRolloutID, "rollout", "", "rollout ID to pause")
	releasePauseCmd.Flags().StringVar(&releaseNote, "note", "", "optional pause note to persist on the paused wave")
	releaseResumeCmd := &cobra.Command{
		Use:   "resume <rollout-id>",
		Short: "Resume a paused grouped release rollout wave",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _, err := loadHarnessProject()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				releaseRolloutID = args[0]
			}
			rollout, err := harness.ResumeReleaseWaveRollout(project, releaseRolloutID, releaseNote)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatReleaseWavePlan(rollout)+"\n")
			return nil
		},
	}
	releaseResumeCmd.Flags().StringVar(&releaseRolloutID, "rollout", "", "rollout ID to resume")
	releaseResumeCmd.Flags().StringVar(&releaseNote, "note", "", "optional resume note to persist on the resumed wave")
	releaseAbortCmd := &cobra.Command{
		Use:   "abort <rollout-id>",
		Short: "Abort the remaining waves in a grouped release rollout",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _, err := loadHarnessProject()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				releaseRolloutID = args[0]
			}
			rollout, err := harness.AbortReleaseWaveRollout(project, releaseRolloutID, releaseNote)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatReleaseWavePlan(rollout)+"\n")
			return nil
		},
	}
	releaseAbortCmd.Flags().StringVar(&releaseRolloutID, "rollout", "", "rollout ID to abort")
	releaseAbortCmd.Flags().StringVar(&releaseNote, "note", "", "optional abort note to persist on aborted waves")
	releaseApproveCmd := &cobra.Command{
		Use:   "approve <rollout-id>",
		Short: "Approve a planned rollout wave so it can be activated",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _, err := loadHarnessProject()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				releaseRolloutID = args[0]
			}
			rollout, err := harness.ApproveReleaseWaveGate(project, releaseRolloutID, releaseWaveOrder, releaseNote)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatReleaseWavePlan(rollout)+"\n")
			return nil
		},
	}
	releaseApproveCmd.Flags().StringVar(&releaseRolloutID, "rollout", "", "rollout ID to approve")
	releaseApproveCmd.Flags().IntVar(&releaseWaveOrder, "wave", 0, "optional rollout wave order to approve; defaults to the next planned wave")
	releaseApproveCmd.Flags().StringVar(&releaseNote, "note", "", "optional approval note to persist on the gated wave")
	releaseRejectCmd := &cobra.Command{
		Use:   "reject <rollout-id>",
		Short: "Reject a planned rollout wave so it cannot be activated",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _, err := loadHarnessProject()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				releaseRolloutID = args[0]
			}
			rollout, err := harness.RejectReleaseWaveGate(project, releaseRolloutID, releaseWaveOrder, releaseNote)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatReleaseWavePlan(rollout)+"\n")
			return nil
		},
	}
	releaseRejectCmd.Flags().StringVar(&releaseRolloutID, "rollout", "", "rollout ID to reject")
	releaseRejectCmd.Flags().IntVar(&releaseWaveOrder, "wave", 0, "optional rollout wave order to reject; defaults to the next planned wave")
	releaseRejectCmd.Flags().StringVar(&releaseNote, "note", "", "optional rejection note to persist on the gated wave")
	releaseCmd.AddCommand(releaseApplyCmd, releaseRolloutsCmd, releaseAdvanceCmd, releasePauseCmd, releaseResumeCmd, releaseAbortCmd, releaseApproveCmd, releaseRejectCmd)

	gcCmd := &cobra.Command{
		Use:   "gc",
		Short: "Archive stale harness runs and prune old logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, cfg, err := loadHarnessProject()
			if err != nil {
				return err
			}
			report, err := harness.RunGC(project, cfg, time.Now().UTC())
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatGCReport(report)+"\n")
			return nil
		},
	}

	doctorCmd := &cobra.Command{
		Use:   "doctor",
		Short: "Inspect harness health, structure, and recent task state",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, cfg, err := loadHarnessProject()
			if err != nil {
				return err
			}
			report, err := harness.Doctor(project, cfg)
			if err != nil {
				return err
			}
			_, _ = writeCLIText(cmd.OutOrStdout(), harness.FormatDoctorReport(report)+"\n")
			return nil
		},
	}

	cmd.AddCommand(initCmd, checkCmd, queueCmd, runCmd, rerunCmd, tasksCmd, monitorCmd, contextsCmd, inboxCmd, reviewCmd, promoteCmd, releaseCmd, gcCmd, doctorCmd)
	return cmd
}

func runHarnessMonitor(out io.Writer, project harness.Project, opts harness.MonitorOptions, watch bool, interval time.Duration) error {
	clearScreen := writerIsTerminal(out)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	for iteration := 0; ; iteration++ {
		report, err := harness.BuildMonitorReport(project, opts)
		if err != nil {
			return err
		}
		if watch {
			if clearScreen {
				_, _ = io.WriteString(out, "\x1b[H\x1b[2J")
			} else if iteration > 0 {
				_, _ = io.WriteString(out, "\n---\n")
			}
		}
		_, _ = writeCLIText(out, harness.FormatMonitorReport(report)+"\n")
		if !watch {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

func writerIsTerminal(w io.Writer) bool {
	fder, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	return term.IsTerminal(int(fder.Fd()))
}

func loadHarnessProject() (harness.Project, *harness.Config, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return harness.Project{}, nil, fmt.Errorf("resolve working directory: %w", err)
	}
	project, err := harness.Discover(workDir)
	if err != nil {
		return harness.Project{}, nil, err
	}
	cfg, err := harness.LoadConfig(project.ConfigPath)
	if err != nil {
		return harness.Project{}, nil, err
	}
	return project, cfg, nil
}

func formatInitResult(result *harness.InitResult) string {
	if result == nil {
		return "Harness init did not produce a result."
	}
	var b strings.Builder
	b.WriteString("Harness initialized.\n")
	if result.GitInitialized {
		b.WriteString("- git: initialized repository\n")
	}
	b.WriteString(fmt.Sprintf("- root: %s\n", result.Project.RootDir))
	b.WriteString(fmt.Sprintf("- config: %s\n", relOrAbs(result.Project.RootDir, result.Project.ConfigPath)))
	for _, path := range result.CreatedPaths {
		b.WriteString(fmt.Sprintf("- created: %s\n", relOrAbs(result.Project.RootDir, path)))
	}
	for _, path := range result.Overwritten {
		b.WriteString(fmt.Sprintf("- overwritten: %s\n", relOrAbs(result.Project.RootDir, path)))
	}
	return strings.TrimRight(b.String(), "\n")
}

func relOrAbs(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func harnessFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
