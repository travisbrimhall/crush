package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/projects"
	"github.com/spf13/cobra"
)

func init() {
	summarizeCmd.Flags().IntP("limit", "n", 10, "Number of sessions to summarize")
	summarizeCmd.Flags().Bool("dry-run", false, "Show what would be summarized without executing")
	summarizeCmd.Flags().Bool("all", false, "Summarize all sessions without summaries")
	rootCmd.AddCommand(summarizeCmd)
}

var summarizeCmd = &cobra.Command{
	Use:   "summarize",
	Short: "Generate summaries for past sessions",
	Long: `Generate AI summaries for past sessions that don't have summaries.

Summaries are stored in the database and used to provide context
in future sessions. This helps the AI understand what you've worked
on previously.

Example:
  # Show sessions that need summarization
  crush summarize --dry-run

  # Summarize the 5 most recent unsummarized sessions
  crush summarize -n 5

  # Summarize all unsummarized sessions
  crush summarize --all
`,
	RunE: runSummarize,
}

func runSummarize(cmd *cobra.Command, args []string) error {
	limit, _ := cmd.Flags().GetInt("limit")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	all, _ := cmd.Flags().GetBool("all")
	debug, _ := cmd.Flags().GetBool("debug")

	if all {
		limit = 1000 // Effectively unlimited.
	}

	cwd, err := ResolveCwd(cmd)
	if err != nil {
		return err
	}

	cfg, err := config.Init(cwd, "", debug)
	if err != nil {
		return err
	}

	if err := createDotCrushDir(cfg.Options.DataDirectory); err != nil {
		return err
	}

	if err := projects.Register(cwd, cfg.Options.DataDirectory); err != nil {
		slog.Warn("Failed to register project", "error", err)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Minute)
	defer cancel()

	// Connect to DB.
	conn, err := db.Connect(ctx, cfg.Options.DataDirectory)
	if err != nil {
		return fmt.Errorf("failed to connect to DB: %w", err)
	}

	q := db.New(conn)

	// Find sessions without summaries.
	sessions, err := q.ListSessions(ctx)
	if err != nil {
		return fmt.Errorf("failed to list sessions: %w", err)
	}

	summaries, err := q.ListSessionSummaries(ctx)
	if err != nil {
		return fmt.Errorf("failed to list summaries: %w", err)
	}

	// Build set of sessions with summaries.
	hasSummary := make(map[string]bool)
	for _, s := range summaries {
		hasSummary[s.SessionID] = true
	}

	// Filter to sessions without summaries and with enough messages.
	var toSummarize []db.Session
	for _, s := range sessions {
		if !hasSummary[s.ID] && s.MessageCount >= 4 {
			toSummarize = append(toSummarize, s)
			if len(toSummarize) >= limit {
				break
			}
		}
	}

	if len(toSummarize) == 0 {
		fmt.Println("No sessions need summarization.")
		return nil
	}

	fmt.Printf("Found %d sessions to summarize:\n\n", len(toSummarize))
	for i, s := range toSummarize {
		fmt.Printf("  %d. %s (%d messages)\n", i+1, s.Title, s.MessageCount)
	}
	fmt.Println()

	if dryRun {
		fmt.Println("Dry run - no summaries generated.")
		return nil
	}

	// Create app instance for summarization.
	appInstance, err := app.New(ctx, conn, cfg)
	if err != nil {
		return fmt.Errorf("failed to create app: %w", err)
	}
	defer appInstance.Shutdown()

	// Summarize each session.
	succeeded := 0
	for i, s := range toSummarize {
		fmt.Printf("Summarizing [%d/%d] %s...\n", i+1, len(toSummarize), s.Title)

		if err := appInstance.AgentCoordinator.Summarize(ctx, s.ID); err != nil {
			fmt.Printf("  Error: %v\n", err)
			continue
		}

		succeeded++
		fmt.Println("  Done")
	}

	fmt.Printf("\nSummarized %d/%d sessions.\n", succeeded, len(toSummarize))
	return nil
}
