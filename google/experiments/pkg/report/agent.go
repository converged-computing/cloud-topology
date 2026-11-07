package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
)

// AgentRun is the agent's accounting for one run, from agent-result.json.
type AgentRun struct {
	// AttemptsUsed is the number of capture attempts the agent spent.
	AttemptsUsed int `json:"attempts_used"`
	// Turns, APIMillis and CostUSD come from the Agent SDK's final result message.
	Turns     int     `json:"turns,omitempty"`
	APIMillis int     `json:"api_ms,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitempty"`
	// IsError is set when the SDK reported the run as failed (for example, not logged in).
	IsError bool `json:"is_error,omitempty"`

	results []agentResult
}

// agentResult is one machine type in the summary topology-agent run prints last.
type agentResult struct {
	MachineType string `json:"machine_type"`
	Outcome     string `json:"outcome"`
	Error       string `json:"error"`
	Attempts    []struct {
		Zone    string `json:"zone"`
		Model   string `json:"model"`
		Outcome string `json:"outcome"`
	} `json:"attempts"`
}

var resultField = regexp.MustCompile(`\b(num_turns|duration_api_ms|total_cost_usd|is_error)=([^,)]+)`)

// readAgentResult reads the stdout of `topology-agent run`: one line per Agent SDK message,
// then a JSON summary starting on a line of its own. A missing file returns nil; a file
// without a summary (a run that never started) returns only what the messages carry.
func readAgentResult(path string) (*AgentRun, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	run := &AgentRun{}
	body := data
	if i := bytes.LastIndex(data, []byte("\n{\n")); i >= 0 {
		body = data[:i]
		var summary struct {
			Results      []agentResult `json:"results"`
			AttemptsUsed int           `json:"attempts_used"`
		}
		if err := json.Unmarshal(data[i+1:], &summary); err != nil {
			return nil, fmt.Errorf("%s: agent summary: %w", path, err)
		}
		run.results, run.AttemptsUsed = summary.Results, summary.AttemptsUsed
	}
	// The SDK's ResultMessage is the last message; read its accounting fields.
	if i := bytes.LastIndex(body, []byte("ResultMessage(")); i >= 0 {
		for _, m := range resultField.FindAllSubmatch(body[i:], -1) {
			v := string(m[2])
			switch string(m[1]) {
			case "num_turns":
				run.Turns, _ = strconv.Atoi(v)
			case "duration_api_ms":
				run.APIMillis, _ = strconv.Atoi(v)
			case "total_cost_usd":
				run.CostUSD, _ = strconv.ParseFloat(v, 64)
			case "is_error":
				run.IsError = v == "True"
			}
		}
	}
	return run, nil
}
