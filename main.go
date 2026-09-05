package main

import (
	"claude-squad/app"
	cmd2 "claude-squad/cmd"
	"claude-squad/config"
	"claude-squad/daemon"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/session/tmux"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/spf13/cobra"
)

// forkRepoURL is this fork, which is where a build of it can be looked up.
const forkRepoURL = "https://github.com/cruzccl/claude-squad"

var (
	// upstreamVersion is the release of smtg-ai/claude-squad this fork was taken from. It
	// is not the version of this binary: builds come from whatever source is in the
	// working tree, so what identifies one is its commit, not this number.
	upstreamVersion = "1.0.20"
	programFlag     string
	autoYesFlag     bool
	daemonFlag      bool
	binName         string
	rootCmd         = &cobra.Command{
		Use:   "claude-squad",
		Short: "Claude Squad - Manage multiple AI agents like Claude Code, Aider, Codex, and Amp.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			log.Initialize(daemonFlag)
			defer log.Close()

			if daemonFlag {
				cfg := config.LoadConfig()
				err := daemon.RunDaemon(cfg)
				log.ErrorLog.Printf("failed to start daemon %v", err)
				return err
			}

			// Check if we're in a git repository
			currentDir, err := filepath.Abs(".")
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}

			if !git.IsGitRepo(currentDir) {
				return fmt.Errorf("error: %s must be run from within a git repository", binName)
			}

			cfg := config.LoadConfig()

			// Program flag overrides config
			program := cfg.GetProgram()
			if programFlag != "" {
				program = programFlag
			}
			// AutoYes flag overrides config
			autoYes := cfg.AutoYes
			if autoYesFlag {
				autoYes = true
			}
			if autoYes {
				defer func() {
					if err := daemon.LaunchDaemon(); err != nil {
						log.ErrorLog.Printf("failed to launch daemon: %v", err)
					}
				}()
			}
			// Kill any daemon that's running.
			if err := daemon.StopDaemon(); err != nil {
				log.ErrorLog.Printf("failed to stop daemon: %v", err)
			}

			return app.Run(ctx, program, autoYes)
		},
	}

	resetCmd = &cobra.Command{
		Use:   "reset",
		Short: "Reset all stored instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()

			state := config.LoadState()
			storage, err := session.NewStorage(state)
			if err != nil {
				return fmt.Errorf("failed to initialize storage: %w", err)
			}
			if err := storage.DeleteAllInstances(); err != nil {
				return fmt.Errorf("failed to reset storage: %w", err)
			}
			fmt.Println("Storage has been reset successfully")

			if err := tmux.CleanupSessions(cmd2.MakeExecutor()); err != nil {
				return fmt.Errorf("failed to cleanup tmux sessions: %w", err)
			}
			fmt.Println("Tmux sessions have been cleaned up")

			if err := git.CleanupWorktrees(); err != nil {
				return fmt.Errorf("failed to cleanup worktrees: %w", err)
			}
			fmt.Println("Worktrees have been cleaned up")

			// Kill any daemon that's running.
			if err := daemon.StopDaemon(); err != nil {
				return err
			}
			fmt.Println("daemon has been stopped")

			return nil
		},
	}

	debugCmd = &cobra.Command{
		Use:   "debug",
		Short: "Print debug information like config paths",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()

			cfg := config.LoadConfig()

			configDir, err := config.GetConfigDir()
			if err != nil {
				return fmt.Errorf("failed to get config directory: %w", err)
			}
			configJson, _ := json.MarshalIndent(cfg, "", "  ")

			fmt.Printf("Config: %s\n%s\n", filepath.Join(configDir, config.ConfigFileName), configJson)

			return nil
		},
	}

	versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Print the version number",
		Run: func(cmd *cobra.Command, args []string) {
			revision, modified, built, ok := buildRevision()
			if !ok {
				// Nothing here identifies the code this was built from, so claim nothing
				// beyond where the fork started.
				fmt.Printf("%s (forked from claude-squad %s)\n", binName, upstreamVersion)
				return
			}

			build := fmt.Sprintf("%s+%s", upstreamVersion, shortRevision(revision))
			if modified {
				// install.sh builds the working tree, so a binary can correspond to no
				// commit at all. Say so rather than name a commit it does not match.
				build += "-dirty"
			}
			if date, err := time.Parse(time.RFC3339, built); err == nil {
				fmt.Printf("%s version %s (%s)\n", binName, build, date.Format("2006-01-02"))
			} else {
				fmt.Printf("%s version %s\n", binName, build)
			}
			fmt.Printf("%s/commit/%s\n", forkRepoURL, revision)
		},
	}
)

// buildRevision reports the commit this binary was built from, whether the working tree
// carried uncommitted changes at the time, and when that commit was made. Go records all
// three whenever it builds from a git checkout, which is how this fork is installed, so
// none of it has to be passed in at build time. ok is false when it did not record them —
// a build from the module cache, or one passed -buildvcs=false.
func buildRevision() (revision string, modified bool, built string, ok bool) {
	info, available := debug.ReadBuildInfo()
	if !available {
		return "", false, "", false
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		case "vcs.time":
			built = setting.Value
		}
	}
	return revision, modified, built, revision != ""
}

// shortRevision abbreviates a commit hash to the length git itself tends to print.
func shortRevision(revision string) string {
	const short = 7
	if len(revision) <= short {
		return revision
	}
	return revision[:short]
}

func init() {
	rootCmd.Flags().StringVarP(&programFlag, "program", "p", "",
		"Program to run in new instances (e.g. 'aider --model ollama_chat/gemma3:1b')")
	rootCmd.Flags().BoolVarP(&autoYesFlag, "autoyes", "y", false,
		"[experimental] If enabled, all instances will automatically accept prompts")
	rootCmd.Flags().BoolVar(&daemonFlag, "daemon", false, "Run a program that loads all sessions"+
		" and runs autoyes mode on them.")

	// Hide the daemonFlag as it's only for internal use
	err := rootCmd.Flags().MarkHidden("daemon")
	if err != nil {
		panic(err)
	}

	rootCmd.AddCommand(debugCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(resetCmd)
}

func main() {
	// Extract the binary name from how this was invoked
	binName = filepath.Base(os.Args[0])
	rootCmd.Use = binName

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
	}
}
