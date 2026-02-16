package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/projects"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func init() {
	daemonCmd.Flags().StringP("tasks", "t", "", "Path to tasks file (YAML)")
	daemonCmd.Flags().DurationP("interval", "i", 5*time.Minute, "Default interval between task runs")
	daemonCmd.Flags().Bool("once", false, "Run all tasks once and exit")
	rootCmd.AddCommand(daemonCmd)
}

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run Crush as a background daemon",
	Long: `Run Crush as a persistent daemon that executes scheduled tasks.

The daemon runs continuously, executing tasks on a schedule.
Tasks are defined in a YAML file.

Example tasks.yaml:
  tasks:
    - name: "Check for test failures"
      interval: "30m"
      prompt: "Check ~/git/myproject for failing tests. If any fail, use the remember tool to store which tests failed."
    
    - name: "Morning summary"
      interval: "24h"
      prompt: "Summarize what I worked on yesterday based on git logs in ~/git"

Example:
  # Run daemon continuously
  crush daemon -t ~/.config/crush/tasks.yaml

  # Run all tasks once (for testing)
  crush daemon -t ~/.config/crush/tasks.yaml --once
`,
	RunE: runDaemon,
}

// DaemonTask represents a scheduled task.
type DaemonTask struct {
	Name     string `yaml:"name"`
	Interval string `yaml:"interval"` // duration string like "30m", "1h", "24h"
	Prompt   string `yaml:"prompt"`
	Enabled  *bool  `yaml:"enabled"` // nil means enabled
}

// DaemonTasksConfig holds the tasks configuration.
type DaemonTasksConfig struct {
	Tasks []DaemonTask `yaml:"tasks"`
}

type taskState struct {
	task    DaemonTask
	lastRun time.Time
	interval time.Duration
}

func runDaemon(cmd *cobra.Command, args []string) error {
	tasksFile, _ := cmd.Flags().GetString("tasks")
	defaultInterval, _ := cmd.Flags().GetDuration("interval")
	once, _ := cmd.Flags().GetBool("once")
	debug, _ := cmd.Flags().GetBool("debug")

	if tasksFile == "" {
		return fmt.Errorf("tasks file required: use -t flag")
	}

	// Load tasks.
	tasksCfg, err := loadTasks(tasksFile)
	if err != nil {
		return fmt.Errorf("failed to load tasks: %w", err)
	}

	if len(tasksCfg.Tasks) == 0 {
		return fmt.Errorf("no tasks defined in %s", tasksFile)
	}

	cwd, err := ResolveCwd(cmd)
	if err != nil {
		return err
	}

	cfg, err := config.Init(cwd, "", debug)
	if err != nil {
		return err
	}

	// Daemon always runs in auto-accept mode.
	if cfg.Permissions == nil {
		cfg.Permissions = &config.Permissions{}
	}
	cfg.Permissions.AllowAll = true

	if err := createDotCrushDir(cfg.Options.DataDirectory); err != nil {
		return err
	}

	if err := projects.Register(cwd, cfg.Options.DataDirectory); err != nil {
		slog.Warn("Failed to register project", "error", err)
	}

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	// Connect to DB.
	conn, err := db.Connect(ctx, cfg.Options.DataDirectory)
	if err != nil {
		return fmt.Errorf("failed to connect to DB: %w", err)
	}

	appInstance, err := app.New(ctx, conn, cfg)
	if err != nil {
		return fmt.Errorf("failed to create app: %w", err)
	}
	defer appInstance.Shutdown()

	// Initialize task states.
	states := make([]taskState, 0, len(tasksCfg.Tasks))
	for _, t := range tasksCfg.Tasks {
		if t.Enabled != nil && !*t.Enabled {
			continue
		}
		interval := defaultInterval
		if t.Interval != "" {
			if parsed, err := time.ParseDuration(t.Interval); err == nil {
				interval = parsed
			}
		}
		states = append(states, taskState{
			task:     t,
			interval: interval,
			lastRun:  time.Time{}, // Never run.
		})
	}

	fmt.Printf("Crush daemon started with %d tasks\n", len(states))
	for _, s := range states {
		fmt.Printf("  - %s (every %s)\n", s.task.Name, s.interval)
	}

	if once {
		// Run all tasks once and exit.
		for i := range states {
			runTask(ctx, appInstance, &states[i], cfg)
		}
		return nil
	}

	// Handle shutdown.
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(10 * time.Second) // Check every 10 seconds.
	defer ticker.Stop()

	for {
		select {
		case <-done:
			fmt.Println("Daemon shutting down...")
			return nil
		case <-ticker.C:
			now := time.Now()
			for i := range states {
				if now.Sub(states[i].lastRun) >= states[i].interval {
					runTask(ctx, appInstance, &states[i], cfg)
				}
			}
		}
	}
}

func loadTasks(path string) (*DaemonTasksConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg DaemonTasksConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func runTask(ctx context.Context, appInstance *app.App, state *taskState, cfg *config.Config) {
	state.lastRun = time.Now()
	fmt.Printf("[%s] Running task: %s\n", state.lastRun.Format("15:04:05"), state.task.Name)
	slog.Info("Running daemon task", "name", state.task.Name)

	// Run the prompt non-interactively.
	err := appInstance.RunNonInteractive(ctx, io.Discard, state.task.Prompt, "", "", true)
	if err != nil {
		slog.Error("Task failed", "name", state.task.Name, "error", err)
		fmt.Printf("[%s] Task failed: %s - %v\n", time.Now().Format("15:04:05"), state.task.Name, err)
	} else {
		fmt.Printf("[%s] Task completed: %s\n", time.Now().Format("15:04:05"), state.task.Name)
	}
}
