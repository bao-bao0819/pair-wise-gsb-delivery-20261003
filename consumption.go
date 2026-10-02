package paseto

import (
	"errors"
	"fmt"
	"time"

	"aidanwoods.dev/go-result/result"
)

// ErrJTIAlreadyConsumed indicates that a token's "jti" was already marked as
// used. It is distinct from a failure to access the consumption store.
var ErrJTIAlreadyConsumed = errors.New("this jti has already been consumed")

// ConsumptionStore atomically records that a one-time token's "jti" has been
// used. ConsumeJTI must return consumed=true only for the first call for a
// given jti, and consumed=false when the jti has already been consumed. A
// non-nil error represents a storage failure and must be reported separately
// from reuse.
//
// The returned result of a successful Parse...AndConsume call is already
// committed to the store. If the caller cannot perform its subsequent business
// operation, the library cannot roll that consumption back; callers must either
// coordinate the store write with their business transaction or treat the
// token as having been consumed.
type ConsumptionStore interface {
	ConsumeJTI(jti string, expiresAt time.Time) (consumed bool, err error)
}

// ConsumptionStoreError wraps any error returned by a ConsumptionStore. It is
// distinct from ErrJTIAlreadyConsumed.
type ConsumptionStoreError struct {
	e error
}

func newConsumptionStoreError(e error) ConsumptionStoreError {
	return ConsumptionStoreError{e}
}

func (e ConsumptionStoreError) Error() string {
	return fmt.Sprintf("could not consume jti: %s", e.e)
}

func (ConsumptionStoreError) Is(e error) bool {
	_, ok1 := e.(ConsumptionStoreError)
	_, ok2 := e.(*ConsumptionStoreError)
	return ok1 || ok2
}

func (e ConsumptionStoreError) Unwrap() error {
	return e.e
}

func (p Parser) parseAndConsume(
	parse func() result.Result[Token],
	store ConsumptionStore,
) (*Token, error) {
	verified := parse()
	validated := result.FlatMap(verified, p.validate)
	tokenResult := result.FlatMap(validated, func(token Token) result.Result[Token] {
		return consumeOneTimeToken(store, token)
	})

	return unwrapTokenResult(tokenResult)
}

func consumeOneTimeToken(store ConsumptionStore, token Token) result.Result[Token] {
	if store == nil {
		return result.Err[Token](newConsumptionStoreError(errors.New("consumption store must not be nil")))
	}

	jti, err := token.GetJti()
	if err != nil {
		return result.Err[Token](newRuleError(fmt.Errorf("one-time token requires a valid jti: %w", err)))
	}
	if jti == "" {
		return result.Err[Token](newRuleError(errors.New("one-time token requires a non-empty jti")))
	}

	expiresAt, err := token.GetExpiration()
	if err != nil {
		return result.Err[Token](newRuleError(fmt.Errorf("one-time token requires a valid expiration: %w", err)))
	}
	if expiresAt.IsZero() {
		return result.Err[Token](newRuleError(errors.New("one-time token requires a non-zero expiration")))
	}
	if time.Now().After(expiresAt) {
		return result.Err[Token](newRuleError(errors.New("one-time token has expired")))
	}

	consumed, err := store.ConsumeJTI(jti, expiresAt)
	if err != nil {
		return result.Err[Token](newConsumptionStoreError(err))
	}
	if !consumed {
		return result.Err[Token](ErrJTIAlreadyConsumed)
	}

	return result.Ok(token)
}

func unwrapTokenResult(tokenResult result.Result[Token]) (*Token, error) {
	var token Token
	if err := tokenResult.Ok(&token); err != nil {
		return nil, err
	}

	return &token, nil
}
