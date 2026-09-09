package appruntime

import (
	"context"
	"fmt"
)

// EffectResult reports the outcome of one effect in a batch.
type EffectResult struct {
	Effect    Effect
	Executed  bool
	Duplicate bool
	Err       error
}

// BatchResult reports a batch execution. ExecuteEffects uses fail-fast
// semantics: execution stops at the first error. Effects before the failure
// have already been executed, so a failed batch is a partial-success fact.
type BatchResult struct {
	Results  []EffectResult
	Executed int
	Failed   int
}

// ExecuteEffects runs a batch with fail-fast semantics. It returns the
// partial-success facts on failure so callers can observe which effects ran
// before the first failure.
func (r *Runtime) ExecuteEffects(ctx context.Context, tenantID, instanceID string, effects []Effect) (BatchResult, error) {
	var res BatchResult
	rec, err := r.instance(tenantID, instanceID)
	if err != nil {
		return res, err
	}
	if err := rec.ensureRunning(); err != nil {
		return res, err
	}
	for _, effect := range effects {
		rr := r.executeOnce(rec, effect)
		res.Results = append(res.Results, rr)
		if rr.Executed {
			res.Executed++
		}
		if rr.Err != nil {
			res.Failed++
			return res, rr.Err
		}
	}
	return res, nil
}

func (r *Runtime) executeOnce(rec *instanceRecord, effect Effect) EffectResult {
	if effect.TenantID != rec.spec.TenantID {
		return EffectResult{Effect: effect, Err: fmt.Errorf("%w: effect tenant %q, instance tenant %q", ErrTenantMismatch, effect.TenantID, rec.spec.TenantID)}
	}
	if err := effect.Validate(); err != nil {
		return EffectResult{Effect: effect, Err: err}
	}
	rec.mu.Lock()
	if rec.executed[effect.IdempotencyKey] {
		rec.mu.Unlock()
		return EffectResult{Effect: effect, Duplicate: true}
	}
	rec.executed[effect.IdempotencyKey] = true
	rec.mu.Unlock()

	err := r.opts.Executor.Execute(recCtx(rec), effect)
	return EffectResult{Effect: effect, Executed: true, Err: err}
}
