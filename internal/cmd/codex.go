package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/codexlog"
	"github.com/steveyegge/gastown/internal/ui"
)

var codexJSON bool
var codexLatest bool

var codexCmd = &cobra.Command{
	Use:         "codex",
	GroupID:     GroupDiag,
	Annotations: map[string]string{AnnotationPolecatSafe: "true"},
	Short:       "Inspect local Codex session artifacts",
}

var codexTranscriptCmd = &cobra.Command{
	Use:         "transcript [path]",
	Aliases:     []string{"analyze"},
	Args:        cobra.MaximumNArgs(1),
	Annotations: map[string]string{AnnotationPolecatSafe: "true"},
	Short:       "Analyze a Codex JSONL transcript turn by turn",
	Long: `Analyze a Codex session JSONL transcript and report:
- exact turn-level token usage from token_count events
- event type counts
- tool call summaries
- estimated context-heavy contributors such as base instructions, injected user instructions, tool outputs, assistant text, and reasoning blocks

If no path is provided, the latest file under ~/.codex/sessions is used.`,
	RunE: runCodexTranscript,
}

func init() {
	rootCmd.AddCommand(codexCmd)
	codexCmd.AddCommand(codexTranscriptCmd)
	codexTranscriptCmd.Flags().BoolVar(&codexJSON, "json", false, "Output machine-readable JSON")
	codexTranscriptCmd.Flags().BoolVar(&codexLatest, "latest", false, "Force lookup of the latest transcript under ~/.codex/sessions")
}

func runCodexTranscript(cmd *cobra.Command, args []string) error {
	path, err := resolveCodexTranscriptPath(args)
	if err != nil {
		return err
	}

	report, err := (&codexlog.Analyzer{}).AnalyzePath(path)
	if err != nil {
		return err
	}

	if codexJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	printCodexTranscript(report)
	return nil
}

func resolveCodexTranscriptPath(args []string) (string, error) {
	if len(args) > 0 && !codexLatest {
		info, err := os.Stat(args[0])
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", args[0], err)
		}
		if info.IsDir() {
			return codexlog.ResolveLatestSessionPath(args[0])
		}
		return args[0], nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	root := filepath.Join(home, ".codex", "sessions")
	return codexlog.ResolveLatestSessionPath(root)
}

func printCodexTranscript(report *codexlog.Report) {
	fmt.Printf("%s %s\n", ui.RenderAccent("Transcript:"), report.Path)
	if report.SessionID != "" {
		fmt.Printf("%s %s\n", ui.RenderAccent("Session:"), report.SessionID)
	}
	if report.Model != "" {
		fmt.Printf("%s %s\n", ui.RenderAccent("Model:"), report.Model)
	}
	if report.CLI != "" {
		fmt.Printf("%s %s\n", ui.RenderAccent("CLI:"), report.CLI)
	}
	fmt.Printf("%s %d lines, %d turns\n", ui.RenderAccent("Shape:"), report.TotalLines, len(report.Turns))
	fmt.Println()

	fmt.Printf("%-4s %-22s %7s %6s %8s %8s %8s %8s %8s %6s\n", "TURN", "STARTED", "EVENTS", "TOOLS", "INPUT", "CACHED", "OUTPUT", "REASON", "TOTAL", "LEFT%")
	for _, turn := range report.Turns {
		started := turn.StartedAt
		if len(started) > 22 {
			started = started[:22]
		}
		leftPct := "-"
		if turn.HasContextLeft {
			leftPct = fmt.Sprintf("%d", turn.ContextLeftPct)
		}
		fmt.Printf("%-4d %-22s %7d %6d %8d %8d %8d %8d %8d %6s\n",
			turn.Index,
			started,
			turn.EventCount,
			turn.ToolCalls,
			turn.TokenUsage.InputTokens,
			turn.TokenUsage.CachedInputTokens,
			turn.TokenUsage.OutputTokens,
			turn.TokenUsage.ReasoningOutputTokens,
			turn.TokenUsage.TotalTokens,
			leftPct,
		)
	}
	fmt.Printf("%-4s %-22s %7s %6s %8d %8d %8d %8d %8d %6s\n", "SUM", "-", "-", "-", report.Totals.InputTokens, report.Totals.CachedInputTokens, report.Totals.OutputTokens, report.Totals.ReasoningOutputTokens, report.Totals.TotalTokens, "-")
	fmt.Println()

	fmt.Println(ui.RenderAccent("Event Summary"))
	for _, event := range report.EventCounts {
		fmt.Printf("  %-36s %6d\n", event.Name, event.Count)
	}
	fmt.Println()

	fmt.Println(ui.RenderAccent("Tool Summary"))
	if len(report.ToolSummaries) == 0 {
		fmt.Println("  (no tool calls)")
	} else {
		for _, tool := range report.ToolSummaries {
			fmt.Printf("  %-24s calls=%-4d args~=%-6d out~=%-6d\n", tool.Name, tool.Calls, tool.EstimatedArgTokens, tool.EstimatedOutTokens)
		}
	}
	fmt.Println()

	fmt.Println(ui.RenderAccent("Context Contributors"))
	fmt.Printf("  %-28s ~%-8d turns=%d\n", "base instructions", report.Contributors.BaseInstructions.ApproxTokens, report.Contributors.BaseInstructions.Occurrences)
	fmt.Printf("  %-28s ~%-8d events=%d\n", "injected user instructions", report.Contributors.InjectedUserInstructions.ApproxTokens, report.Contributors.InjectedUserInstructions.Occurrences)
	fmt.Printf("  %-28s ~%-8d items=%d\n", "assistant text", report.Contributors.AssistantText.ApproxTokens, report.Contributors.AssistantText.Occurrences)
	fmt.Printf("  %-28s ~%-8d items=%d\n", "tool outputs", report.Contributors.ToolOutputs.ApproxTokens, report.Contributors.ToolOutputs.Occurrences)
	fmt.Printf("  %-28s %-9d exact\n", "reasoning tokens", report.Contributors.ReasoningTokens.ApproxTokens)
	fmt.Println()

	fmt.Println(ui.RenderAccent("Top Context Contributors"))
	if len(report.TopContributors) == 0 {
		fmt.Println("  (no detailed contributors)")
	} else {
		for _, item := range report.TopContributors {
			where := ""
			if item.TurnIndex > 0 {
				where = fmt.Sprintf("turn=%d", item.TurnIndex)
			}
			if item.Tool != "" {
				if where != "" {
					where += " "
				}
				where += "tool=" + item.Tool
			}
			if item.Occurrences > 1 {
				if where != "" {
					where += " "
				}
				where += fmt.Sprintf("x%d", item.Occurrences)
			}
			if where == "" {
				where = "-"
			}
			fmt.Printf("  ~%-8d %-28s %-22s %s\n", item.ApproxTokens, item.Kind, where, item.Snippet)
		}
	}
	fmt.Println()

	fmt.Println(ui.RenderAccent("Repeated Prompt Load"))
	fmt.Printf("  base instructions per turn: %d\n", report.RepeatedPromptLoad.BaseInstructionsPerTurn)
	fmt.Printf("  injected instructions avg:  %d\n", report.RepeatedPromptLoad.InjectedPerTurnAverage)
	fmt.Printf("  approximate repeated total: %d\n", report.RepeatedPromptLoad.ApproxTokens)
}
