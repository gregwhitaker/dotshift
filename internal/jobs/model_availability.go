package jobs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/modelpolicy"
)

// Availability is observed API health, not a prediction of remaining credits.
// It is shared by strategies using the same provider configuration and model.
type Availability struct {
	config.ModelRoute
	Available bool       `json:"available"`
	Reason    string     `json:"reason,omitempty"`
	RetryAt   *time.Time `json:"retry_at,omitempty"`
}

func (r *Runner) availabilityScopes(route config.ModelRoute) (provider, model string) {
	p := r.Config.Providers[route.Provider]
	// Do not persist secret values. A changed endpoint or credential variable
	// creates a new scope. A key rotated in the same variable can be retried
	// explicitly with resume_goal(retry_unavailable=true).
	identity := fmt.Sprintf("%s\x00%s\x00%s", route.Provider, p.BaseURL, p.APIKeyEnv)
	if r.Config.UsesSubscriptions() {
		identity = "subscription\x00" + route.Provider
	}
	provider = fmt.Sprintf("provider:%x", sha256.Sum256([]byte(identity)))
	return provider, provider + ":model:" + route.Model
}

func (r *Runner) modelAvailability(ctx context.Context, route config.ModelRoute, now time.Time) (Availability, error) {
	result := Availability{ModelRoute: route, Available: true}
	provider, model := r.availabilityScopes(route)
	for _, scope := range []string{provider, model} {
		var reason string
		var retry int64
		err := r.Store.db.QueryRowContext(ctx, `SELECT reason, retry_at FROM model_availability WHERE scope = ?`, scope).Scan(&reason, &retry)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return result, err
		}
		at := fromUnix(retry)
		if retry != 0 && !at.After(now) {
			continue
		}
		if result.Available || (result.RetryAt != nil && (retry == 0 || at.After(*result.RetryAt))) {
			result.Available, result.Reason, result.RetryAt = false, reason, nil
			if retry != 0 {
				result.RetryAt = &at
			}
		}
	}
	return result, nil
}

func (r *Runner) recordModelFailure(ctx context.Context, route config.ModelRoute, failure *modelpolicy.Failure) error {
	provider, model := r.availabilityScopes(route)
	scope := model
	if failure.ProviderWide {
		scope = provider
	}
	_, err := r.Store.db.ExecContext(ctx, `INSERT INTO model_availability (scope, reason, retry_at, recorded_at)
		VALUES (?, ?, ?, ?) ON CONFLICT(scope) DO UPDATE SET
		reason = excluded.reason, recorded_at = excluded.recorded_at,
		retry_at = CASE WHEN model_availability.retry_at = 0 OR excluded.retry_at = 0 THEN 0
		ELSE MAX(model_availability.retry_at, excluded.retry_at) END`, scope, failure.Kind, unix(failure.RetryAt), unix(time.Now()))
	return err
}

func (r *Runner) resetModelAvailability(ctx context.Context, routes []config.ModelRoute) error {
	tx, err := r.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, route := range routes {
		provider, model := r.availabilityScopes(route)
		if _, err := tx.ExecContext(ctx, `DELETE FROM model_availability WHERE scope IN (?, ?)`, provider, model); err != nil {
			return err
		}
	}
	return tx.Commit()
}
