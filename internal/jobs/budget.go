package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/modelpolicy"
	"github.com/gregwhitaker/dotshift/internal/usage"
)

const usdUnits = 1e9 // nanodollars; rounding never drops a positive receipt to zero

var errCostPending = errors.New("waiting for a model cost receipt")

type BudgetProgress struct {
	LimitUSD        float64      `json:"limit_usd"`
	SpentUSD        float64      `json:"spent_usd"`
	RemainingUSD    float64      `json:"remaining_usd"`
	Requests        int          `json:"requests"`
	PendingRequests int          `json:"pending_requests"`
	Models          []ModelSpend `json:"models"`
}

type ModelSpend struct {
	Model    string  `json:"model"`
	Role     string  `json:"role"`
	SpentUSD float64 `json:"spent_usd"`
	Requests int     `json:"requests"`
}

func budgetUnits(usd float64) (int64, error) {
	if err := config.ValidateMaxCost(usd); err != nil {
		return 0, err
	}
	return int64(math.Ceil(usd * usdUnits)), nil
}

func (s *Store) budget(ctx context.Context, id string) (*BudgetProgress, error) {
	var limit, spent int64
	var calls, pending int
	err := s.db.QueryRowContext(ctx, `SELECT limit_units,
		COALESCE((SELECT SUM(cost_units) FROM goal_model_calls WHERE job_id = ?), 0),
		(SELECT COUNT(*) FROM goal_model_calls WHERE job_id = ?),
		(SELECT COUNT(*) FROM goal_model_calls WHERE job_id = ? AND cost_units IS NULL)
		FROM goal_budgets WHERE job_id = ?`, id, id, id, id).Scan(&limit, &spent, &calls, &pending)
	if err != nil {
		return nil, fmt.Errorf("reading project budget: %w", err)
	}
	b := &BudgetProgress{LimitUSD: float64(limit) / usdUnits, SpentUSD: float64(spent) / usdUnits, Requests: calls, PendingRequests: pending, Models: []ModelSpend{}}
	b.RemainingUSD = max(0, b.LimitUSD-b.SpentUSD)
	rows, err := s.db.QueryContext(ctx, `SELECT model, role, COALESCE(SUM(cost_units),0), COUNT(*) FROM goal_model_calls WHERE job_id = ? GROUP BY model, role ORDER BY model, role`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m ModelSpend
		var units int64
		if err := rows.Scan(&m.Model, &m.Role, &units, &m.Requests); err != nil {
			return nil, err
		}
		m.SpentUSD = float64(units) / usdUnits
		b.Models = append(b.Models, m)
	}
	return b, rows.Err()
}

func (s *Store) refreshBudget(ctx context.Context, j *Job) error {
	if j.Goal == nil || j.Goal.Budget == nil {
		return nil
	}
	b, err := s.budget(ctx, j.ID)
	if err == nil {
		j.Goal.Budget = b
	}
	return err
}

func (r *Runner) validateGoalBudget(limit float64) error {
	if err := config.ValidateMaxCost(limit); err != nil {
		return err
	}
	if limit > 0 && r.Config.UsesSubscriptions() {
		return errors.New("subscription mode cannot meter USD spending; use max_iterations and jobs.timeout instead of max_cost_usd")
	}
	if limit > 0 && !r.Config.Router.UsageEnabled() {
		return errors.New("project spending caps require router usage recording; restore the usage receiver before resuming")
	}
	return nil
}

// admitModelCall is one atomic check-and-insert. A capped goal has at most
// one unsettled request, including across processes and service restarts.
func (s *Store) admitModelCall(ctx context.Context, jobID, token, model, role string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(random[:])
	result, err := s.db.ExecContext(ctx, `INSERT INTO goal_model_calls (id, job_id, run_token, model, role, created_at)
		SELECT ?, ?, ?, ?, ?, ? FROM goal_budgets b
		WHERE b.job_id = ? AND EXISTS(SELECT 1 FROM jobs WHERE id = ? AND run_token = ? AND status = ?)
		AND (b.limit_units = 0 OR (
		COALESCE((SELECT SUM(cost_units) FROM goal_model_calls WHERE job_id = b.job_id),0) < b.limit_units
		AND NOT EXISTS(SELECT 1 FROM goal_model_calls WHERE job_id = b.job_id AND cost_units IS NULL)))`,
		id, jobID, token, model, role, unix(time.Now()), jobID, jobID, token, Running)
	if err != nil {
		return "", err
	}
	if n, err := result.RowsAffected(); err != nil {
		return "", err
	} else if n == 1 {
		return id, nil
	}
	var owner string
	var status Status
	if err := s.db.QueryRowContext(ctx, `SELECT run_token, status FROM jobs WHERE id = ?`, jobID).Scan(&owner, &status); err != nil {
		return "", err
	}
	if owner != token || status != Running {
		return "", errGoalStopped
	}
	b, err := s.budget(ctx, jobID)
	if err != nil {
		return "", err
	}
	if b.LimitUSD > 0 && b.SpentUSD >= b.LimitUSD {
		return "", fmt.Errorf("project spending cap reached: reported $%.6f of $%.6f; resume_goal can change max_cost_usd after the user approves a new limit", b.SpentUSD, b.LimitUSD)
	}
	return "", errCostPending
}

// recordCost is idempotent across streamed receipts and callback redelivery.
// A later receipt can correct a cost upwards, but cannot erase recorded spend.
func (s *Store) recordCost(ctx context.Context, id string, cost float64, input, output int) error {
	units, err := budgetUnits(cost)
	if err != nil || input < 0 || output < 0 {
		return errors.New("invalid model cost receipt")
	}
	_, err = s.db.ExecContext(ctx, `UPDATE goal_model_calls SET
		cost_units = MAX(COALESCE(cost_units,0), ?), input_tokens = MAX(input_tokens, ?), output_tokens = MAX(output_tokens, ?)
		WHERE id = ?`, units, input, output, id)
	return err
}

// RecordUsage receives the authenticated LiteLLM callback. Unknown request IDs
// do not create charges; only calls admitted by the goal gateway can be billed.
func (s *Store) RecordUsage(ctx context.Context, records []usage.Record) error {
	for _, record := range records {
		if record.RequestID != "" && record.CostKnown {
			if err := s.recordCost(ctx, record.RequestID, record.Cost, record.InputTokens, record.OutputTokens); err != nil {
				return err
			}
		}
	}
	return nil
}

type goalAccountant struct {
	store                     *Store
	jobID, token, model, role string
	timeout                   time.Duration
}

func (a *goalAccountant) Begin(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, a.waitTimeout())
	defer cancel()
	for {
		id, err := a.store.admitModelCall(ctx, a.jobID, a.token, a.model, a.role)
		if !errors.Is(err, errCostPending) {
			return id, err
		}
		if err := waitAccounting(ctx); err != nil {
			return "", errors.New("model spending is unconfirmed; waiting for the previous request's cost receipt before sending more requests")
		}
	}
}

func (a *goalAccountant) Record(id string, cost float64, input, output int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return a.store.recordCost(ctx, id, cost, input, output)
}

func (a *goalAccountant) Finish(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, a.waitTimeout())
	defer cancel()
	for {
		var cost sql.NullInt64
		if err := a.store.db.QueryRowContext(ctx, `SELECT cost_units FROM goal_model_calls WHERE id = ?`, id).Scan(&cost); err != nil {
			return err
		}
		if cost.Valid {
			return nil
		}
		var limit int64
		if err := a.store.db.QueryRowContext(ctx, `SELECT limit_units FROM goal_budgets WHERE job_id = ?`, a.jobID).Scan(&limit); err != nil {
			return err
		}
		if limit == 0 {
			return nil
		}
		if err := waitAccounting(ctx); err != nil {
			return errors.New("model cost receipt is missing; project spending remains unconfirmed until the router reports it")
		}
	}
}

func (a *goalAccountant) waitTimeout() time.Duration {
	if a.timeout > 0 {
		return a.timeout
	}
	return 15 * time.Second
}

func waitAccounting(ctx context.Context) error {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var _ modelpolicy.Accountant = (*goalAccountant)(nil)
