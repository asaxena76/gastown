package codexlog

import (
	"os"
	"strings"
	"testing"
)

func TestAnalyzerSummarizesTurnsToolsAndContributors(t *testing.T) {
	transcript := strings.Join([]string{
		`{"timestamp":"2026-03-06T00:00:00Z","type":"session_meta","payload":{"id":"sess-1","timestamp":"2026-03-06T00:00:00Z","cwd":"/repo","cli_version":"0.1.0","source":"cli","instructions":"Injected user instructions","base_instructions":{"text":"Base instructions here"}}}`,
		`{"timestamp":"2026-03-06T00:00:01Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-03-06T00:00:02Z","type":"turn_context","payload":{"turn_id":"turn-1","cwd":"/repo","model":"gpt-5.3-codex","user_instructions":"Injected user instructions"}}`,
		`{"timestamp":"2026-03-06T00:00:03Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}"}}`,
		`{"timestamp":"2026-03-06T00:00:04Z","type":"response_item","payload":{"type":"function_call_output","output":"Chunk ID: x\nOutput:\n/repo\n"}}`,
		`{"timestamp":"2026-03-06T00:00:05Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":120},"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":120},"model_context_window":240}}}`,
		`{"timestamp":"2026-03-06T00:00:05Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":120},"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":120},"model_context_window":240}}}`,
		`{"timestamp":"2026-03-06T00:00:06Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first answer"}]}}`,
		`{"timestamp":"2026-03-06T00:01:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}`,
		`{"timestamp":"2026-03-06T00:01:01Z","type":"turn_context","payload":{"turn_id":"turn-2","cwd":"/repo","model":"gpt-5.3-codex","user_instructions":"Injected user instructions"}}`,
		`{"timestamp":"2026-03-06T00:01:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":260,"cached_input_tokens":120,"output_tokens":30,"reasoning_output_tokens":7,"total_tokens":290},"last_token_usage":{"input_tokens":160,"cached_input_tokens":80,"output_tokens":10,"reasoning_output_tokens":2,"total_tokens":170},"model_context_window":340}}}`,
		`{"timestamp":"2026-03-06T00:01:03Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"second answer"}]}}`,
	}, "\n")

	report, err := (&Analyzer{}).Analyze(strings.NewReader(transcript))
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	if report.SessionID != "sess-1" {
		t.Fatalf("SessionID = %q, want %q", report.SessionID, "sess-1")
	}
	if len(report.Turns) != 2 {
		t.Fatalf("len(Turns) = %d, want 2", len(report.Turns))
	}
	if report.Turns[0].TokenUsage.TotalTokens != 120 {
		t.Fatalf("turn 1 total_tokens = %d, want 120", report.Turns[0].TokenUsage.TotalTokens)
	}
	if report.Turns[0].EventCount != 7 {
		t.Fatalf("turn 1 event_count = %d, want 7", report.Turns[0].EventCount)
	}
	if !report.Turns[0].HasContextLeft || report.Turns[0].ContextLeftPct != 50 {
		t.Fatalf("turn 1 context_left_pct = %d, want 50", report.Turns[0].ContextLeftPct)
	}
	if report.Turns[1].TokenUsage.TotalTokens != 170 {
		t.Fatalf("turn 2 total_tokens = %d, want 170", report.Turns[1].TokenUsage.TotalTokens)
	}
	if report.Turns[1].EventCount != 4 {
		t.Fatalf("turn 2 event_count = %d, want 4", report.Turns[1].EventCount)
	}
	if !report.Turns[1].HasContextLeft || report.Turns[1].ContextLeftPct != 50 {
		t.Fatalf("turn 2 context_left_pct = %d, want 50", report.Turns[1].ContextLeftPct)
	}
	if report.Totals.TotalTokens != 290 {
		t.Fatalf("Totals.TotalTokens = %d, want 290", report.Totals.TotalTokens)
	}
	if len(report.ToolSummaries) != 1 {
		t.Fatalf("len(ToolSummaries) = %d, want 1", len(report.ToolSummaries))
	}
	if report.ToolSummaries[0].Name != "exec_command" || report.ToolSummaries[0].Calls != 1 {
		t.Fatalf("tool summary = %+v, want exec_command x1", report.ToolSummaries[0])
	}
	if report.Contributors.ReasoningTokens.ApproxTokens != 7 {
		t.Fatalf("ReasoningTokens = %d, want 7", report.Contributors.ReasoningTokens.ApproxTokens)
	}
	if report.Contributors.ToolOutputs.ApproxTokens == 0 {
		t.Fatal("ToolOutputs.ApproxTokens = 0, want > 0")
	}
	if report.Contributors.AssistantText.ApproxTokens == 0 {
		t.Fatal("AssistantText.ApproxTokens = 0, want > 0")
	}
	if report.RepeatedPromptLoad.Turns != 2 {
		t.Fatalf("RepeatedPromptLoad.Turns = %d, want 2", report.RepeatedPromptLoad.Turns)
	}
	if len(report.TopContributors) == 0 {
		t.Fatal("TopContributors = 0, want > 0")
	}
	foundToolOutput := false
	for _, item := range report.TopContributors {
		if item.Kind == "tool_output" && item.TurnIndex == 1 {
			foundToolOutput = true
			break
		}
	}
	if !foundToolOutput {
		t.Fatal("TopContributors missing tool_output for turn 1")
	}
}

func TestResolveLatestSessionPathFindsNewestJSONL(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepathJoin(t, dir, "2026", "03", "05", "old.jsonl")
	newPath := filepathJoin(t, dir, "2026", "03", "06", "new.jsonl")
	writeFile(t, oldPath, "{}\n")
	writeFile(t, newPath, "{}\n")

	if err := os.Chtimes(oldPath, fixedTime(1), fixedTime(1)); err != nil {
		t.Fatalf("Chtimes old: %v", err)
	}
	if err := os.Chtimes(newPath, fixedTime(2), fixedTime(2)); err != nil {
		t.Fatalf("Chtimes new: %v", err)
	}

	got, err := ResolveLatestSessionPath(dir)
	if err != nil {
		t.Fatalf("ResolveLatestSessionPath() error = %v", err)
	}
	if got != newPath {
		t.Fatalf("ResolveLatestSessionPath() = %q, want %q", got, newPath)
	}
}

func TestAnalyzerIncludesCustomToolCallsInToolSummary(t *testing.T) {
	transcript := strings.Join([]string{
		`{"timestamp":"2026-03-06T00:00:00Z","type":"session_meta","payload":{"id":"sess-2","timestamp":"2026-03-06T00:00:00Z","cwd":"/repo","cli_version":"0.1.0","source":"cli","base_instructions":{"text":"Base"}}}`,
		`{"timestamp":"2026-03-06T00:00:01Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-03-06T00:00:02Z","type":"turn_context","payload":{"turn_id":"turn-1","cwd":"/repo","model":"gpt-5.3-codex"}}`,
		`{"timestamp":"2026-03-06T00:00:03Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\n*** End Patch\n"}}`,
		`{"timestamp":"2026-03-06T00:00:04Z","type":"response_item","payload":{"type":"custom_tool_call_output","output":"{\"output\":\"Success\"}"}}`,
	}, "\n")

	report, err := (&Analyzer{}).Analyze(strings.NewReader(transcript))
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	if len(report.ToolSummaries) != 1 {
		t.Fatalf("len(ToolSummaries) = %d, want 1", len(report.ToolSummaries))
	}
	if report.ToolSummaries[0].Name != "apply_patch" {
		t.Fatalf("ToolSummaries[0].Name = %q, want %q", report.ToolSummaries[0].Name, "apply_patch")
	}
	if report.ToolSummaries[0].Calls != 1 {
		t.Fatalf("ToolSummaries[0].Calls = %d, want 1", report.ToolSummaries[0].Calls)
	}
}
