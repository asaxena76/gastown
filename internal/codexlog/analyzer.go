package codexlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type Report struct {
	Path                string              `json:"path"`
	SessionID           string              `json:"session_id,omitempty"`
	CWD                 string              `json:"cwd,omitempty"`
	Model               string              `json:"model,omitempty"`
	CLI                 string              `json:"cli_version,omitempty"`
	StartedAt           string              `json:"started_at,omitempty"`
	Source              string              `json:"source,omitempty"`
	Turns               []TurnReport        `json:"turns"`
	EventCounts         []NamedCount        `json:"event_counts"`
	ToolSummaries       []ToolSummary       `json:"tool_summaries"`
	Contributors        ContributorSummary  `json:"contributors"`
	TopContributors     []ContributorDetail `json:"top_contributors,omitempty"`
	Totals              Usage               `json:"totals"`
	TotalLines          int                 `json:"total_lines"`
	RepeatedPromptLoad  PromptLoadEstimate  `json:"repeated_prompt_load"`
	SessionInstruction  InstructionEstimate `json:"session_instruction"`
	BaseInstructionSize InstructionEstimate `json:"base_instruction_size"`
}

type TurnReport struct {
	Index          int    `json:"index"`
	ID             string `json:"id,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
	CWD            string `json:"cwd,omitempty"`
	Model          string `json:"model,omitempty"`
	EventCount     int    `json:"event_count"`
	ToolCalls      int    `json:"tool_calls"`
	TokenUsage     Usage  `json:"token_usage"`
	ContextWindow  int    `json:"context_window,omitempty"`
	ContextLeftPct int    `json:"context_left_pct,omitempty"`
	LastTool       string `json:"last_tool,omitempty"`
	HasUsage       bool   `json:"has_usage"`
	HasContextLeft bool   `json:"has_context_left"`
	PromptBytes    int    `json:"prompt_bytes,omitempty"`
	Notes          string `json:"notes,omitempty"`
}

type Usage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
	TotalTokens           int `json:"total_tokens"`
}

type NamedCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type ToolSummary struct {
	Name                string `json:"name"`
	Calls               int    `json:"calls"`
	EstimatedArgTokens  int    `json:"estimated_arg_tokens"`
	EstimatedOutTokens  int    `json:"estimated_output_tokens"`
	EstimatedArgBytes   int    `json:"estimated_arg_bytes"`
	EstimatedOutputByte int    `json:"estimated_output_bytes"`
}

type ContributorSummary struct {
	BaseInstructions         Estimate `json:"base_instructions"`
	InjectedUserInstructions Estimate `json:"injected_user_instructions"`
	AssistantText            Estimate `json:"assistant_text"`
	ToolOutputs              Estimate `json:"tool_outputs"`
	ReasoningTokens          Estimate `json:"reasoning_tokens"`
}

type Estimate struct {
	ApproxTokens int    `json:"approx_tokens"`
	Occurrences  int    `json:"occurrences"`
	Mode         string `json:"mode"`
}

type PromptLoadEstimate struct {
	BaseInstructionsPerTurn int `json:"base_instructions_per_turn"`
	InjectedPerTurnAverage  int `json:"injected_user_instructions_avg_per_turn"`
	Turns                   int `json:"turns"`
	ApproxTokens            int `json:"approx_tokens"`
}

type InstructionEstimate struct {
	ApproxTokens int `json:"approx_tokens"`
	Bytes        int `json:"bytes"`
}

type ContributorDetail struct {
	Kind         string `json:"kind"`
	ApproxTokens int    `json:"approx_tokens"`
	Bytes        int    `json:"bytes"`
	TurnID       string `json:"turn_id,omitempty"`
	TurnIndex    int    `json:"turn_index,omitempty"`
	Tool         string `json:"tool,omitempty"`
	Occurrences  int    `json:"occurrences,omitempty"`
	Snippet      string `json:"snippet,omitempty"`
}

type Analyzer struct{}

func (a *Analyzer) AnalyzePath(path string) (*Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	report, err := a.Analyze(f)
	if err != nil {
		return nil, err
	}
	report.Path = path
	return report, nil
}

func (a *Analyzer) Analyze(r io.Reader) (*Report, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 8*1024*1024)

	state := analyzerState{
		report: &Report{},
		turns:  map[string]*turnState{},
		tools:  map[string]*toolState{},
		events: map[string]int{},
	}

	for scanner.Scan() {
		state.report.TotalLines++
		line := scanner.Bytes()
		if len(bytesTrimSpace(line)) == 0 {
			continue
		}
		if err := state.consume(line); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan transcript: %w", err)
	}

	return state.finalize(), nil
}

func ResolveLatestSessionPath(root string) (string, error) {
	var latestPath string
	var latestTime time.Time

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".jsonl") {
			return nil
		}
		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latestPath = path
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk %s: %w", root, err)
	}
	if latestPath == "" {
		return "", fmt.Errorf("no .jsonl files found under %s", root)
	}
	return latestPath, nil
}

type analyzerState struct {
	report       *Report
	currentTurn  *turnState
	turns        map[string]*turnState
	turnOrder    []*turnState
	tools        map[string]*toolState
	events       map[string]int
	baseText     string
	sessionInstr string
	top          []ContributorDetail
}

type turnState struct {
	report               TurnReport
	injectedUserTokens   int
	injectedOccurrences  int
	informativeSnapshots map[string]struct{}
}

type toolState struct {
	name       string
	calls      int
	argTokens  int
	outTokens  int
	argBytes   int
	outputByte int
}

type envelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMetaPayload struct {
	ID               string `json:"id"`
	Timestamp        string `json:"timestamp"`
	CWD              string `json:"cwd"`
	CLI              string `json:"cli_version"`
	Source           string `json:"source"`
	Instructions     string `json:"instructions"`
	BaseInstructions struct {
		Text string `json:"text"`
	} `json:"base_instructions"`
}

type turnContextPayload struct {
	TurnID           string `json:"turn_id"`
	CWD              string `json:"cwd"`
	Model            string `json:"model"`
	CurrentDate      string `json:"current_date"`
	Timezone         string `json:"timezone"`
	UserInstructions string `json:"user_instructions"`
}

type eventPayload struct {
	Type      string         `json:"type"`
	TurnID    string         `json:"turn_id"`
	Message   string         `json:"message"`
	Info      *tokenInfo     `json:"info"`
	RateLimit map[string]any `json:"rate_limits"`
}

type tokenInfo struct {
	TotalTokenUsage tokenUsage `json:"total_token_usage"`
	LastTokenUsage  tokenUsage `json:"last_token_usage"`
	ModelWindow     int        `json:"model_context_window"`
}

type tokenUsage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
	TotalTokens           int `json:"total_tokens"`
}

type responsePayload struct {
	Type      string        `json:"type"`
	Role      string        `json:"role"`
	Name      string        `json:"name"`
	Arguments string        `json:"arguments"`
	Input     string        `json:"input"`
	CallID    string        `json:"call_id"`
	Output    string        `json:"output"`
	Content   []contentItem `json:"content"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (s *analyzerState) consume(line []byte) error {
	var env envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return fmt.Errorf("decode line %d: %w", s.report.TotalLines, err)
	}

	switch env.Type {
	case "session_meta":
		s.events["session_meta"]++
		return s.consumeSessionMeta(env.Payload)
	case "turn_context":
		s.events["turn_context"]++
		return s.consumeTurnContext(env.Timestamp, env.Payload)
	case "event_msg":
		return s.consumeEvent(env.Timestamp, env.Payload)
	case "response_item":
		return s.consumeResponseItem(env.Payload)
	default:
		s.events[env.Type]++
	}

	return nil
}

func (s *analyzerState) consumeSessionMeta(raw json.RawMessage) error {
	var payload sessionMetaPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode session_meta payload: %w", err)
	}

	s.report.SessionID = payload.ID
	s.report.CWD = payload.CWD
	s.report.CLI = payload.CLI
	s.report.Source = payload.Source
	s.report.StartedAt = payload.Timestamp
	s.baseText = payload.BaseInstructions.Text
	s.sessionInstr = payload.Instructions
	s.report.BaseInstructionSize = InstructionEstimate{
		ApproxTokens: estimateTokens(payload.BaseInstructions.Text),
		Bytes:        len(payload.BaseInstructions.Text),
	}
	s.report.SessionInstruction = InstructionEstimate{
		ApproxTokens: estimateTokens(payload.Instructions),
		Bytes:        len(payload.Instructions),
	}
	return nil
}

func (s *analyzerState) consumeTurnContext(ts string, raw json.RawMessage) error {
	var payload turnContextPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode turn_context payload: %w", err)
	}

	turn := s.ensureTurn(payload.TurnID, ts)
	turn.report.EventCount++
	turn.report.CWD = payload.CWD
	turn.report.Model = payload.Model
	s.currentTurn = turn
	if s.report.Model == "" && payload.Model != "" {
		s.report.Model = payload.Model
	}

	if payload.UserInstructions != "" {
		tokens := estimateTokens(payload.UserInstructions)
		turn.injectedUserTokens += tokens
		turn.injectedOccurrences++
		s.recordContributor("injected_user_instructions", tokens, len(payload.UserInstructions), turn.report.ID, "", 1, payload.UserInstructions)
	}

	return nil
}

func (s *analyzerState) consumeEvent(ts string, raw json.RawMessage) error {
	var payload eventPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode event payload: %w", err)
	}
	key := "event_msg:" + payload.Type
	s.events[key]++

	switch payload.Type {
	case "task_started":
		turn := s.ensureTurn(payload.TurnID, ts)
		turn.report.EventCount++
		s.currentTurn = turn
	case "token_count":
		if s.currentTurn != nil {
			s.currentTurn.report.EventCount++
		}
		if payload.Info == nil || s.currentTurn == nil {
			return nil
		}
		usage := Usage{
			InputTokens:           payload.Info.LastTokenUsage.InputTokens,
			CachedInputTokens:     payload.Info.LastTokenUsage.CachedInputTokens,
			OutputTokens:          payload.Info.LastTokenUsage.OutputTokens,
			ReasoningOutputTokens: payload.Info.LastTokenUsage.ReasoningOutputTokens,
			TotalTokens:           payload.Info.LastTokenUsage.TotalTokens,
		}
		s.currentTurn.recordUsage(usage)
		s.currentTurn.recordContextWindow(payload.Info.ModelWindow)
	default:
		if s.currentTurn != nil {
			s.currentTurn.report.EventCount++
		}
	}

	return nil
}

func (s *analyzerState) consumeResponseItem(raw json.RawMessage) error {
	var payload responsePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode response_item payload: %w", err)
	}

	key := "response_item:" + payload.Type
	if payload.Type == "message" && payload.Role != "" {
		key += ":" + payload.Role
	}
	s.events[key]++
	if s.currentTurn != nil {
		s.currentTurn.report.EventCount++
	}

	switch payload.Type {
	case "function_call":
		s.recordToolCall(payload.Name, payload.Arguments)
	case "custom_tool_call":
		s.recordToolCall(payload.Name, payload.Input)
	case "function_call_output":
		s.recordToolOutput(payload.Output)
	case "custom_tool_call_output":
		s.recordToolOutput(payload.Output)
	case "message":
		if payload.Role == "assistant" {
			for _, item := range payload.Content {
				if item.Type == "output_text" {
					s.report.Contributors.AssistantText.ApproxTokens += estimateTokens(item.Text)
					s.report.Contributors.AssistantText.Occurrences++
					turnID := ""
					if s.currentTurn != nil {
						turnID = s.currentTurn.report.ID
					}
					s.recordContributor("assistant_text", estimateTokens(item.Text), len(item.Text), turnID, "", 1, item.Text)
				}
			}
		}
	}

	return nil
}

func (s *analyzerState) ensureTurn(id, ts string) *turnState {
	if turn, ok := s.turns[id]; ok {
		return turn
	}

	turn := &turnState{
		report: TurnReport{
			ID:        id,
			StartedAt: ts,
		},
		informativeSnapshots: map[string]struct{}{},
	}
	s.turns[id] = turn
	s.turnOrder = append(s.turnOrder, turn)
	return turn
}

func (s *analyzerState) recordToolCall(name, args string) {
	if name == "" {
		name = "<unknown>"
	}
	tool := s.ensureTool(name)
	tool.calls++
	tool.argBytes += len(args)
	tool.argTokens += estimateTokens(args)
	if s.currentTurn != nil {
		s.currentTurn.report.ToolCalls++
		s.currentTurn.report.LastTool = name
	}
}

func (s *analyzerState) recordToolOutput(output string) {
	tokens := estimateTokens(output)
	s.report.Contributors.ToolOutputs.ApproxTokens += tokens
	s.report.Contributors.ToolOutputs.Occurrences++
	turnID := ""
	toolName := ""
	if s.currentTurn != nil {
		turnID = s.currentTurn.report.ID
		toolName = s.currentTurn.report.LastTool
	}
	s.recordContributor("tool_output", tokens, len(output), turnID, toolName, 1, output)
	if s.currentTurn == nil || s.currentTurn.report.LastTool == "" {
		return
	}
	tool := s.ensureTool(s.currentTurn.report.LastTool)
	tool.outputByte += len(output)
	tool.outTokens += tokens
}

func (s *analyzerState) recordContributor(kind string, tokens, bytes int, turnID, tool string, occurrences int, snippet string) {
	if tokens <= 0 && bytes <= 0 {
		return
	}
	s.top = append(s.top, ContributorDetail{
		Kind:         kind,
		ApproxTokens: tokens,
		Bytes:        bytes,
		TurnID:       turnID,
		Tool:         tool,
		Occurrences:  occurrences,
		Snippet:      truncateSnippet(snippet, 96),
	})
}

func (s *analyzerState) ensureTool(name string) *toolState {
	if tool, ok := s.tools[name]; ok {
		return tool
	}
	tool := &toolState{name: name}
	s.tools[name] = tool
	return tool
}

func (t *turnState) recordUsage(usage Usage) {
	signature := fmt.Sprintf("%d/%d/%d/%d/%d", usage.InputTokens, usage.CachedInputTokens, usage.OutputTokens, usage.ReasoningOutputTokens, usage.TotalTokens)
	if _, seen := t.informativeSnapshots[signature]; seen {
		return
	}
	t.informativeSnapshots[signature] = struct{}{}

	t.report.HasUsage = true
	if usage.TotalTokens >= t.report.TokenUsage.TotalTokens {
		t.report.TokenUsage = usage
		t.updateContextLeft()
	}
}

func (t *turnState) recordContextWindow(window int) {
	if window <= 0 {
		return
	}
	if window >= t.report.ContextWindow {
		t.report.ContextWindow = window
		t.updateContextLeft()
	}
}

func (t *turnState) updateContextLeft() {
	if t.report.ContextWindow <= 0 || t.report.TokenUsage.TotalTokens <= 0 {
		return
	}
	leftRatio := 1 - (float64(t.report.TokenUsage.TotalTokens) / float64(t.report.ContextWindow))
	leftPct := int(math.Round(leftRatio * 100))
	if leftPct < 0 {
		leftPct = 0
	}
	if leftPct > 100 {
		leftPct = 100
	}
	t.report.ContextLeftPct = leftPct
	t.report.HasContextLeft = true
}

func (s *analyzerState) finalize() *Report {
	for i, turn := range s.turnOrder {
		turn.report.Index = i + 1
		s.report.Turns = append(s.report.Turns, turn.report)
		s.report.Totals.InputTokens += turn.report.TokenUsage.InputTokens
		s.report.Totals.CachedInputTokens += turn.report.TokenUsage.CachedInputTokens
		s.report.Totals.OutputTokens += turn.report.TokenUsage.OutputTokens
		s.report.Totals.ReasoningOutputTokens += turn.report.TokenUsage.ReasoningOutputTokens
		s.report.Totals.TotalTokens += turn.report.TokenUsage.TotalTokens
		s.report.Contributors.InjectedUserInstructions.ApproxTokens += turn.injectedUserTokens
		s.report.Contributors.InjectedUserInstructions.Occurrences += turn.injectedOccurrences
	}

	turnCount := len(s.report.Turns)
	if turnCount == 0 {
		turnCount = 1
	}
	basePerTurn := estimateTokens(s.baseText)
	s.report.Contributors.BaseInstructions = Estimate{
		ApproxTokens: basePerTurn * len(s.report.Turns),
		Occurrences:  len(s.report.Turns),
		Mode:         "estimated",
	}
	if s.report.Contributors.InjectedUserInstructions.Mode == "" {
		s.report.Contributors.InjectedUserInstructions.Mode = "estimated"
	}
	if s.report.Contributors.AssistantText.Mode == "" {
		s.report.Contributors.AssistantText.Mode = "estimated"
	}
	if s.report.Contributors.ToolOutputs.Mode == "" {
		s.report.Contributors.ToolOutputs.Mode = "estimated"
	}
	s.report.Contributors.ReasoningTokens = Estimate{
		ApproxTokens: s.report.Totals.ReasoningOutputTokens,
		Occurrences:  len(s.report.Turns),
		Mode:         "exact",
	}
	s.report.RepeatedPromptLoad = PromptLoadEstimate{
		BaseInstructionsPerTurn: basePerTurn,
		InjectedPerTurnAverage:  divOrZero(s.report.Contributors.InjectedUserInstructions.ApproxTokens, turnCount),
		Turns:                   len(s.report.Turns),
		ApproxTokens:            basePerTurn*len(s.report.Turns) + s.report.Contributors.InjectedUserInstructions.ApproxTokens,
	}

	if s.report.Contributors.InjectedUserInstructions.ApproxTokens == 0 && s.sessionInstr != "" {
		s.report.Contributors.InjectedUserInstructions = Estimate{
			ApproxTokens: estimateTokens(s.sessionInstr),
			Occurrences:  1,
			Mode:         "estimated_fallback",
		}
		s.report.RepeatedPromptLoad.InjectedPerTurnAverage = s.report.Contributors.InjectedUserInstructions.ApproxTokens
		s.report.RepeatedPromptLoad.ApproxTokens = basePerTurn*len(s.report.Turns) + s.report.Contributors.InjectedUserInstructions.ApproxTokens
	}

	if len(s.report.Turns) > 0 && basePerTurn > 0 {
		s.recordContributor("base_instructions_repeated", basePerTurn*len(s.report.Turns), len(s.baseText)*len(s.report.Turns), "", "", len(s.report.Turns), s.baseText)
	}

	s.report.EventCounts = sortedCounts(s.events)
	s.report.ToolSummaries = sortedTools(s.tools)
	s.report.TopContributors = sortedTopContributors(s.top, s.report.Turns)
	return s.report
}

func sortedCounts(counts map[string]int) []NamedCount {
	out := make([]NamedCount, 0, len(counts))
	for name, count := range counts {
		out = append(out, NamedCount{Name: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Name < out[j].Name
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func sortedTools(tools map[string]*toolState) []ToolSummary {
	out := make([]ToolSummary, 0, len(tools))
	for _, tool := range tools {
		out = append(out, ToolSummary{
			Name:                tool.name,
			Calls:               tool.calls,
			EstimatedArgTokens:  tool.argTokens,
			EstimatedOutTokens:  tool.outTokens,
			EstimatedArgBytes:   tool.argBytes,
			EstimatedOutputByte: tool.outputByte,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls == out[j].Calls {
			return out[i].Name < out[j].Name
		}
		return out[i].Calls > out[j].Calls
	})
	return out
}

func sortedTopContributors(items []ContributorDetail, turns []TurnReport) []ContributorDetail {
	if len(items) == 0 {
		return nil
	}
	turnIndex := make(map[string]int, len(turns))
	for _, turn := range turns {
		turnIndex[turn.ID] = turn.Index
	}
	out := append([]ContributorDetail(nil), items...)
	for i := range out {
		if out[i].TurnIndex == 0 && out[i].TurnID != "" {
			out[i].TurnIndex = turnIndex[out[i].TurnID]
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ApproxTokens == out[j].ApproxTokens {
			if out[i].Bytes == out[j].Bytes {
				return out[i].Kind < out[j].Kind
			}
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].ApproxTokens > out[j].ApproxTokens
	})
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func estimateTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	return int(math.Ceil(float64(utf8.RuneCountInString(text)) / 4.0))
}

func divOrZero(total, denom int) int {
	if denom == 0 {
		return 0
	}
	return total / denom
}

func bytesTrimSpace(b []byte) []byte {
	start := 0
	end := len(b)
	for start < end {
		switch b[start] {
		case ' ', '\t', '\n', '\r':
			start++
		default:
			goto right
		}
	}
right:
	for end > start {
		switch b[end-1] {
		case ' ', '\t', '\n', '\r':
			end--
		default:
			return b[start:end]
		}
	}
	return b[start:end]
}

func truncateSnippet(text string, maxRunes int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		return ""
	}
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxRunes-1]) + "…"
}
