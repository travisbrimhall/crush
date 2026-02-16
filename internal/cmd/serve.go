package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/projects"
	"github.com/charmbracelet/crush/internal/ui/common"
	ui "github.com/charmbracelet/crush/internal/ui/model"
	"github.com/gliderlabs/ssh"
	"github.com/spf13/cobra"
	gossh "golang.org/x/crypto/ssh"
)

func init() {
	serveCmd.Flags().StringP("host", "H", "0.0.0.0", "Host to bind to")
	serveCmd.Flags().StringP("port", "p", "2222", "Port to listen on")
	serveCmd.Flags().StringP("key", "k", "", "Path to SSH host key (default: ~/.config/crush/ssh_host_key)")
	rootCmd.AddCommand(serveCmd)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start Crush as an SSH server",
	Long: `Start Crush as a persistent SSH server that accepts connections.

This allows you to SSH into a running Crush instance from anywhere.
Memories and context persist across sessions.

Example:
  # Start server on default port 2222
  crush serve

  # Start on custom port
  crush serve -p 2323

  # Connect from another machine
  ssh -p 2222 user@hostname
`,
	RunE: runServe,
}

func runServe(cmd *cobra.Command, args []string) error {
	host, _ := cmd.Flags().GetString("host")
	port, _ := cmd.Flags().GetString("port")
	keyPath, _ := cmd.Flags().GetString("key")
	debug, _ := cmd.Flags().GetBool("debug")

	// Default key path.
	if keyPath == "" {
		home, _ := os.UserHomeDir()
		keyPath = filepath.Join(home, ".config", "crush", "ssh_host_key")
	}

	// Ensure key directory exists.
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return fmt.Errorf("failed to create key directory: %w", err)
	}

	// Generate host key if it doesn't exist.
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		if err := generateHostKey(keyPath); err != nil {
			return fmt.Errorf("failed to generate host key: %w", err)
		}
		slog.Info("Generated new SSH host key", "path", keyPath)
	}

	cwd, err := ResolveCwd(cmd)
	if err != nil {
		return err
	}

	cfg, err := config.Init(cwd, "", debug)
	if err != nil {
		return err
	}

	// Force allow-all permissions for SSH sessions (no interactive prompts).
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

	// Create the SSH session handler.
	handler := func(s ssh.Session) {
		ctx := s.Context()
		fmt.Printf("New SSH session: user=%s remote=%s\n", s.User(), s.RemoteAddr())
		slog.Info("New SSH session", "user", s.User(), "remote", s.RemoteAddr())

		// Connect to DB for this session.
		conn, err := db.Connect(ctx, cfg.Options.DataDirectory)
		if err != nil {
			fmt.Printf("DB error: %v\n", err)
			slog.Error("Failed to connect to DB", "error", err)
			fmt.Fprintf(s, "Error: %v\n", err)
			return
		}

		fmt.Println("DB connected, creating app...")
		appInstance, err := app.New(ctx, conn, cfg)
		if err != nil {
			fmt.Printf("App error: %v\n", err)
			slog.Error("Failed to create app", "error", err)
			fmt.Fprintf(s, "Error: %v\n", err)
			return
		}
		defer appInstance.Shutdown()
		fmt.Println("App created")

		pty, winCh, isPty := s.Pty()
		if !isPty {
			fmt.Fprintln(s, "PTY required. Use: ssh -t ...")
			return
		}
		fmt.Printf("PTY: %dx%d\n", pty.Window.Width, pty.Window.Height)

		// Create the TUI.
		com := common.DefaultCommon(appInstance)
		model := ui.New(com)
		fmt.Println("Model created, starting program...")

		// Build environment from PTY.
		env := []string{
			fmt.Sprintf("TERM=%s", pty.Term),
			"COLORTERM=truecolor",
			fmt.Sprintf("COLUMNS=%d", pty.Window.Width),
			fmt.Sprintf("LINES=%d", pty.Window.Height),
		}
		// Add session environment.
		for _, e := range s.Environ() {
			env = append(env, e)
		}
		var uvEnv uv.Environ = env

		// Create program with SSH input/output.
		program := tea.NewProgram(
			model,
			tea.WithInput(s),
			tea.WithOutput(s),
			tea.WithEnvironment(uvEnv),
		)

		go appInstance.Subscribe(program)

		// Handle window size changes.
		go func() {
			for win := range winCh {
				program.Send(tea.WindowSizeMsg{
					Width:  win.Width,
					Height: win.Height,
				})
			}
		}()

		// Send initial window size after a brief delay to ensure program is ready.
		go func() {
			time.Sleep(50 * time.Millisecond)
			program.Send(tea.WindowSizeMsg{
				Width:  pty.Window.Width,
				Height: pty.Window.Height,
			})
		}()

		fmt.Println("Running program...")
		if _, err := program.Run(); err != nil {
			fmt.Printf("Program error: %v\n", err)
			slog.Error("TUI error", "error", err)
		}

		slog.Info("SSH session ended", "user", s.User())
	}

	// Create SSH server.
	server := &ssh.Server{
		Addr:    net.JoinHostPort(host, port),
		Handler: handler,
		// Accept all connections for now (local network use).
		// TODO: Add proper auth for production use.
		PublicKeyHandler: func(ctx ssh.Context, key ssh.PublicKey) bool {
			return true
		},
		PasswordHandler: func(ctx ssh.Context, password string) bool {
			return true
		},
	}

	// Load host key.
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("failed to read host key: %w", err)
	}
	signer, err := gossh.ParsePrivateKey(keyBytes)
	if err != nil {
		return fmt.Errorf("failed to parse host key: %w", err)
	}
	server.AddHostKey(signer)

	// Handle shutdown gracefully.
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	slog.Info("Starting Crush SSH server", "address", net.JoinHostPort(host, port))
	fmt.Printf("Crush SSH server listening on %s\n", net.JoinHostPort(host, port))
	fmt.Printf("Connect with: ssh -p %s localhost\n", port)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != ssh.ErrServerClosed {
			slog.Error("SSH server error", "error", err)
			done <- syscall.SIGTERM
		}
	}()

	<-done

	slog.Info("Shutting down SSH server")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return server.Shutdown(ctx)
}

func generateHostKey(path string) error {
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", path, "-N", "", "-q")
	return cmd.Run()
}
