package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/eval"
	"github.com/tripledoublev/v100/internal/ui"
)

func dogfoodCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dogfood",
		Short: "Run v100's self-test quest suite",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(dogfoodRunCmd(cfgPath), dogfoodReportCmd())
	return cmd
}

func dogfoodRunCmd(cfgPath *string) *cobra.Command {
	var dogfoodDir string
	var unsafe bool

	cmd := &cobra.Command{
		Use:   "run [quest...]",
		Short: "Execute dogfood quests and collect scores",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := dogfoodDir
			if dir == "" {
				dir = "dogfood"
			}

			// Discover quests
			quests, err := eval.DiscoverQuests(dir)
			if err != nil {
				return fmt.Errorf("discover quests: %w", err)
			}
			if len(quests) == 0 {
				return fmt.Errorf("no quest files found in %s", dir)
			}

			// Filter if specific quests requested
			if len(args) > 0 {
				quests = eval.FilterQuests(quests, args)
				if len(quests) == 0 {
					return fmt.Errorf("no matching quests found for: %s", strings.Join(args, ", "))
				}
			}

			// Get git commit hash for tagging
			commitHash := getGitCommitShort()

			fmt.Printf("%s Running %d dogfood quest(s)%s\n",
				ui.Header("🐕"), len(quests), commitTag(commitHash))

			opts := benchRunOptions{
				yolo:   true,
				unsafe: unsafe,
			}

			var results []eval.DogfoodResult
			report := eval.DogfoodReport{
				Timestamp: time.Now(),
				Commit:    commitHash,
			}

			for i, quest := range quests {
				fmt.Printf("\n%s Quest %d/%d: %s\n",
					ui.Info("→"), i+1, len(quests), quest.Name)

				start := time.Now()
				result := eval.DogfoodResult{
					Quest: quest,
				}

				// Run the bench config and get the created run ID
				runID, err := runDogfoodQuest(cfgPath, quest, opts, commitHash)
				if err != nil {
					result.Score = "error"
					result.Error = err.Error()
					fmt.Printf("  %s Error: %v\n", ui.Fail("✗"), err)
				} else {
					result.Duration = time.Since(start)
					result.RunID = runID

					// Read the actual score from meta.json (written by scorer)
					if runID != "" {
						meta, err := core.ReadMeta(filepath.Join("runs", runID))
						if err == nil && meta.Score != "" {
							result.Score = strings.ToLower(meta.Score)
						} else {
							// No scorer ran, just completion happened
							result.Score = "pass"
						}
					} else {
						result.Score = "pass"
					}
				}

				results = append(results, result)
				report.Results = results
			}

			// Collect scores from runs
			report = collectScores(report)

			// Print report
			fmt.Printf("\n%s\n", eval.FormatReport(report))

			// Regression detection
			previous, _ := eval.LoadLastDogfoodReport("runs")
			if len(previous) > 0 {
				regressions := eval.DetectRegressions(results, previous)
				if len(regressions) > 0 {
					fmt.Printf("%s Regressions detected: %s\n",
						ui.Fail("⚠"), strings.Join(regressions, ", "))
				}
			}

			// Return non-zero if any failures
			if report.Failed > 0 {
				return fmt.Errorf("%d quest(s) failed", report.Failed)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&dogfoodDir, "dir", "", "dogfood directory (default: ./dogfood)")
	cmd.Flags().BoolVar(&unsafe, "unsafe", false, "disable path guardrails and confirmations")
	return cmd
}

func dogfoodReportCmd() *cobra.Command {
	var runsDir string

	cmd := &cobra.Command{
		Use:   "report",
		Short: "Show results from the last dogfood run",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := runsDir
			if dir == "" {
				dir = "runs"
			}

			results, err := eval.LoadLastDogfoodReport(dir)
			if err != nil {
				return fmt.Errorf("load report: %w", err)
			}
			if len(results) == 0 {
				fmt.Println("No dogfood runs found.")
				return nil
			}

			report := eval.DogfoodReport{
				Timestamp: time.Now(),
				Results:   results,
			}
			report = collectScores(report)

			fmt.Print(eval.FormatReport(report))
			return nil
		},
	}

	cmd.Flags().StringVar(&runsDir, "runs", "", "runs directory (default: ./runs)")
	return cmd
}

// runDogfoodQuest executes a single bench file with dogfood tagging.
// Returns the created run ID and any error.
func runDogfoodQuest(cfgPath *string, quest eval.DogfoodQuest, opts benchRunOptions, commitHash string) (string, error) {
	// Delegate to the existing bench run infrastructure
	return runBenchConfig(cfgPath, quest.File, opts)
}

// collectScores updates report totals from results by looking at actual run scores.
func collectScores(report eval.DogfoodReport) eval.DogfoodReport {
	passed, failed, skipped := 0, 0, 0
	for i, r := range report.Results {
		score := strings.ToLower(r.Score)
		switch score {
		case "pass":
			passed++
		case "fail", "error":
			failed++
		default:
			skipped++
			report.Results[i].Score = "skipped"
		}
	}
	report.Passed = passed
	report.Failed = failed
	report.Skipped = skipped
	report.Total = len(report.Results)
	return report
}

// getGitCommitShort returns the short git commit hash, or empty string.
func getGitCommitShort() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func commitTag(hash string) string {
	if hash == "" {
		return ""
	}
	return fmt.Sprintf(" (commit: %s)", hash)
}
