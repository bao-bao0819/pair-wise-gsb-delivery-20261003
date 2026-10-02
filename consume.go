package paseto

import (
	"errors"

	"aidanwoods.dev/go-result/result"
)

// ConsumedTokenStore is a caller-provided store used to mark one-time tokens
// as consumed, keyed by their "jti" claim.
//
// MarkConsumed MUST atomically mark the given jti as used. When two
// concurrent requests supply the same jti only one of them may observe
// success; the other MUST receive an error for which errors.Is against
// ErrTokenAlreadyConsumed is true.
//
// If the jti has never been seen, it MUST be recorded as consumed and nil
// returned. If it has already been consumed, ErrTokenAlreadyConsumed (or an
// error wrapping it) MUST be returned. Any other failure MUST be returned as
// a distinct error so that callers can separate storage problems from replay
// attempts; such errors will be wrapped in a StoreError.
//
// Implementations are responsible for their own persistence, concurrency
// control, and any retention/expiry policy.
type ConsumedTokenStore interface {
	MarkConsumed(jti string) error
}

// ErrTokenAlreadyConsumed is returned by ConsumedTokenStore.MarkConsumed
// when the given jti has already been marked as consumed. It denotes a replay
// of a one-time token, rather than a storage failure.
var ErrTokenAlreadyConsumed = errors.New("token has already been consumed")

// ErrJtiMissing is returned by a consuming parse when the token carries no
// usable "jti" claim. Such a token cannot be keyed for one-time consumption,
// and the store is not written.
var ErrJtiMissing = errors.New("token is missing a `jti' claim")

// ErrExpirationMissing is returned by a consuming parse when the token carries
// no "exp" claim. One-time tokens must carry an expiration, and the store is
// not written.
var ErrExpirationMissing = errors.New("token is missing an `exp' claim")

// ErrInvalidTime is returned by a consuming parse when a time claim (such as
// "exp") is present but not a valid RFC3339 time. The store is not written.
var ErrInvalidTime = errors.New("token contains an invalid time claim")

// StoreError wraps an error returned by a ConsumedTokenStore that is not a
// duplicate-consumption (replay) error. Use errors.Is against ErrTokenAlreadyConsumed
// to distinguish a replay from a storage failure, and errors.As against
// *StoreError to inspect the underlying storage error.
type StoreError struct {
	e error
}

func newStoreError(e error) error {
	return StoreError{e}
}

func (e StoreError) Error() string {
	return "consumed token store error: " + e.e.Error()
}

func (StoreError) Is(e error) bool {
	_, ok1 := e.(StoreError)
	_, ok2 := e.(*StoreError)
	return ok1 || ok2
}

func (e StoreError) Unwrap() error {
	return e.e
}

// ConsumingParser behaves like a Parser, but additionally marks successfully
// processed one-time tokens as consumed in a caller-provided
// ConsumedTokenStore.
//
// A token is marked consumed only after it has been signed/verified or
// decrypted AND every configured rule has passed. The store is therefore
// never written for malformed, unauthenticated, expired, or otherwise
// rule-rejected tokens. The token must also carry a non-empty "jti" claim and
// a valid "exp" claim; otherwise parsing fails without touching the store.
//
// Semantics the caller MUST understand:
//
// A successful return means the jti has already been recorded as consumed.
// The library cannot roll that marking back, nor can it participate in the
// caller's external business transaction. If the caller subsequently fails to
// carry out the business action, retrying the same token will be reported as
// ErrTokenAlreadyConsumed. Callers that need atomicity between consumption
// and their own state change MUST coordinate that themselves (for example by
// recording the consumed jti within the same transaction as their business
// write). The storage layer is the sole boundary that guarantees at-most-once
// acceptance.
//
// A storage error is returned as a StoreError (distinct from
// ErrTokenAlreadyConsumed); the token was not confirmed consumed, so the
// request may be retried.
type ConsumingParser struct {
	Parser
	store ConsumedTokenStore
}

// NewConsumingParser returns a ConsumingParser with the NotExpired rule
// preloaded and the given store.
func NewConsumingParser(store ConsumedTokenStore) ConsumingParser {
	return ConsumingParser{NewParser(), store}
}

// NewConsumingParserWithoutExpiryCheck returns a ConsumingParser with no
// currently set rules and the given store.
func NewConsumingParserWithoutExpiryCheck(store ConsumedTokenStore) ConsumingParser {
	return ConsumingParser{NewParserWithoutExpiryCheck(), store}
}

// NewConsumingParserForValidNow returns a ConsumingParser that will require
// parsed tokens to be valid "now", using the given store.
func NewConsumingParserForValidNow(store ConsumedTokenStore) ConsumingParser {
	return ConsumingParser{NewParserForValidNow(), store}
}

// MakeConsumingParser constructs a ConsumingParser with the specified rules
// and the given store.
func MakeConsumingParser(rules []Rule, store ConsumedTokenStore) ConsumingParser {
	return ConsumingParser{MakeParser(rules), store}
}

// ParseV2Local verifies and decrypts a v2 local paseto, validates it against
// every configured rule, and marks it consumed. See the ConsumingParser type
// documentation for consumption and failure semantics.
func (p ConsumingParser) ParseV2Local(key V2SymmetricKey, tainted string) (*Token, error) {
	msg := newMessage(V2Local, tainted)
	verified := result.FlatMap(msg, func(m message) result.Result[Token] {
		return m.v2Decrypt(key)
	})

	return p.consume(verified)
}

// ParseV2Public verifies a v2 public paseto, validates it against every
// configured rule, and marks it consumed. See the ConsumingParser type
// documentation for consumption and failure semantics.
func (p ConsumingParser) ParseV2Public(key V2AsymmetricPublicKey, tainted string) (*Token, error) {
	msg := newMessage(V2Public, tainted)
	verified := result.FlatMap(msg, func(m message) result.Result[Token] {
		return m.v2Verify(key)
	})

	return p.consume(verified)
}

// ParseV3Local verifies and decrypts a v3 local paseto, validates it against
// every configured rule, and marks it consumed. See the ConsumingParser type
// documentation for consumption and failure semantics.
func (p ConsumingParser) ParseV3Local(key V3SymmetricKey, tainted string, implicit []byte) (*Token, error) {
	msg := newMessage(V3Local, tainted)
	verified := result.FlatMap(msg, func(m message) result.Result[Token] {
		return m.v3Decrypt(key, implicit)
	})

	return p.consume(verified)
}

// ParseV3Public verifies a v3 public paseto, validates it against every
// configured rule, and marks it consumed. See the ConsumingParser type
// documentation for consumption and failure semantics.
func (p ConsumingParser) ParseV3Public(key V3AsymmetricPublicKey, tainted string, implicit []byte) (*Token, error) {
	msg := newMessage(V3Public, tainted)
	verified := result.FlatMap(msg, func(m message) result.Result[Token] {
		return m.v3Verify(key, implicit)
	})

	return p.consume(verified)
}

// ParseV4Local verifies and decrypts a v4 local paseto, validates it against
// every configured rule, and marks it consumed. See the ConsumingParser type
// documentation for consumption and failure semantics.
func (p ConsumingParser) ParseV4Local(key V4SymmetricKey, tainted string, implicit []byte) (*Token, error) {
	msg := newMessage(V4Local, tainted)
	verified := result.FlatMap(msg, func(m message) result.Result[Token] {
		return m.v4Decrypt(key, implicit)
	})

	return p.consume(verified)
}

// ParseV4Public verifies a v4 public paseto, validates it against every
// configured rule, and marks it consumed. See the ConsumingParser type
// documentation for consumption and failure semantics.
func (p ConsumingParser) ParseV4Public(key V4AsymmetricPublicKey, tainted string, implicit []byte) (*Token, error) {
	msg := newMessage(V4Public, tainted)
	verified := result.FlatMap(msg, func(m message) result.Result[Token] {
		return m.v4Verify(key, implicit)
	})

	return p.consume(verified)
}

// consume runs all configured rules and, only if they pass and the token
// carries a usable jti and a valid exp, marks the token consumed in the store.
func (p ConsumingParser) consume(verified result.Result[Token]) (*Token, error) {
	token := result.FlatMap(verified, p.validate)
	marked := result.FlatMap(token, p.markConsumed)

	var t Token
	if err := marked.Ok(&t); err != nil {
		return nil, err
	}

	return &t, nil
}

// markConsumed validates the claims required to key and bound a one-time
// token, then performs the atomic store write. It is called only after the
// token has been cryptographically verified and every rule has passed.
func (p ConsumingParser) markConsumed(token Token) result.Result[Token] {
	jti, err := token.GetJti()
	if err != nil || jti == "" {
		return result.Err[Token](newRuleError(ErrJtiMissing))
	}

	if _, ok := token.claims["exp"]; !ok {
		return result.Err[Token](newRuleError(ErrExpirationMissing))
	}

	if _, err := token.GetExpiration(); err != nil {
		return result.Err[Token](newRuleError(ErrInvalidTime))
	}

	if err := p.store.MarkConsumed(jti); err != nil {
		if errors.Is(err, ErrTokenAlreadyConsumed) {
			return result.Err[Token](newRuleError(err))
		}

		return result.Err[Token](newStoreError(err))
	}

	return result.Ok(token)
}
