// Package usage records what each model call cost and summarizes it.
//
// LiteLLM's own spend tracking needs a Postgres database. Instead, dotshift
// turns on LiteLLM's built-in generic_api callback, which POSTs a JSON record
// for every call to an HTTP endpoint. `dotshift serve` runs that endpoint
// (Receiver) and appends a small accounting record per call to a JSON Lines
// file next to the config. Prompts and responses are never stored.
package usage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// File is the usage log's name inside the state directory.
const File = "usage.jsonl"

// Record is one model call.
type Record struct {
	Time time.Time `json:"time"`
	// Alias is the alias the client asked for, e.g. "smart".
	Alias string `json:"alias"`
	// Route is the router model name that served the call: the alias for its
	// primary model, or e.g. "smart~1" for its first fallback.
	Route        string  `json:"route"`
	Model        string  `json:"model"` // provider/model
	OK           bool    `json:"ok"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Cost         float64 `json:"cost"` // USD, as LiteLLM priced it
	CostKnown    bool    `json:"cost_known"`
	RequestID    string  `json:"request_id,omitempty"` // dotshift's accounting ID
	Error        string  `json:"error,omitempty"`
}

// payload is the subset of LiteLLM's StandardLoggingPayload dotshift reads.
type payload struct {
	ModelGroup       string   `json:"model_group"`
	Model            string   `json:"model"`
	Status           string   `json:"status"`
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	ResponseCost     *float64 `json:"response_cost"`
	CostFinalized    *bool    `json:"cost_finalized"`
	Metadata         struct {
		SpendLogsMetadata struct {
			RequestID string `json:"dotshift_request_id"`
		} `json:"spend_logs_metadata"`
	} `json:"metadata"`
	StartTime float64 `json:"startTime"`
	ErrorStr  string  `json:"error_str"`
}

var fallbackSuffix = regexp.MustCompile(`~\d+$`)

func (p payload) record() Record {
	sec, frac := math.Modf(p.StartTime)
	r := Record{
		Time:         time.Unix(int64(sec), int64(frac*1e9)).UTC(),
		Alias:        fallbackSuffix.ReplaceAllString(p.ModelGroup, ""),
		Route:        p.ModelGroup,
		Model:        p.Model,
		OK:           p.Status != "failure",
		InputTokens:  p.PromptTokens,
		OutputTokens: p.CompletionTokens,
		RequestID:    p.Metadata.SpendLogsMetadata.RequestID,
	}
	if p.ResponseCost != nil && *p.ResponseCost >= 0 && !math.IsInf(*p.ResponseCost, 0) && !math.IsNaN(*p.ResponseCost) && (p.CostFinalized == nil || *p.CostFinalized) {
		r.Cost, r.CostKnown = *p.ResponseCost, true
	}
	if !r.OK {
		r.Error = firstLine(p.ErrorStr, 200)
	}
	return r
}

func firstLine(s string, max int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if len(s) > max {
		s = s[:max-3] + "..."
	}
	return s
}

// Decode parses a callback body: a JSON array of payloads (LiteLLM's
// default), or one or more payloads separated by newlines.
func Decode(body []byte) ([]Record, error) {
	body = bytes.TrimSpace(body)
	var ps []payload
	if bytes.HasPrefix(body, []byte("[")) {
		if err := json.Unmarshal(body, &ps); err != nil {
			return nil, err
		}
	} else {
		dec := json.NewDecoder(bytes.NewReader(body))
		for {
			var p payload
			if err := dec.Decode(&p); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return nil, err
			}
			ps = append(ps, p)
		}
	}
	out := make([]Record, 0, len(ps))
	for _, p := range ps {
		if p.ModelGroup == "" && p.Model == "" {
			continue
		}
		out = append(out, p.record())
	}
	return out, nil
}

// Receiver is the HTTP endpoint LiteLLM's generic_api callback posts to.
type Receiver struct {
	// Path is the JSON Lines file records are appended to.
	Path string
	// Token must arrive as "Authorization: Bearer <Token>".
	Token string
	// OnRecords durably applies receipts before appending the diagnostic log.
	OnRecords func(context.Context, []Record) error

	mu sync.Mutex
}

func (rc *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if req.Header.Get("Authorization") != "Bearer "+rc.Token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 32<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	records, err := Decode(body)
	if err != nil {
		http.Error(w, "bad payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	if rc.OnRecords != nil {
		if err := rc.OnRecords(req.Context(), records); err != nil {
			http.Error(w, "saving usage receipts failed", http.StatusInternalServerError)
			return
		}
	}
	if err := rc.Append(records); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Append writes records to the log, one JSON object per line.
func (rc *Receiver) Append(records []Record) error {
	if len(records) == 0 {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	f, err := os.OpenFile(rc.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Load reads the usage log, skipping lines it can't parse. A missing file is
// an empty log.
func Load(path string) ([]Record, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			out = append(out, r)
		}
	}
	return out, sc.Err()
}

// Row totals the calls for one alias and model.
type Row struct {
	Alias        string
	Model        string
	Requests     int
	Failures     int
	InputTokens  int
	OutputTokens int
	Cost         float64
}

// Summarize groups records at or after since by alias and model, sorted by
// alias then model. A zero since includes everything.
func Summarize(records []Record, since time.Time) []Row {
	type key struct{ alias, model string }
	rows := map[key]*Row{}
	for _, r := range records {
		if r.Time.Before(since) {
			continue
		}
		k := key{r.Alias, r.Model}
		row := rows[k]
		if row == nil {
			row = &Row{Alias: r.Alias, Model: r.Model}
			rows[k] = row
		}
		row.Requests++
		if !r.OK {
			row.Failures++
		}
		row.InputTokens += r.InputTokens
		row.OutputTokens += r.OutputTokens
		row.Cost += r.Cost
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Alias != out[j].Alias {
			return out[i].Alias < out[j].Alias
		}
		return out[i].Model < out[j].Model
	})
	return out
}

// ParsePeriod parses a look-back period such as "24h", "90m" or "7d".
func ParsePeriod(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("bad period %q (try 24h or 7d)", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("bad period %q (try 24h or 7d)", s)
	}
	return d, nil
}
