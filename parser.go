package paseto

import (
	"time"

	"aidanwoods.dev/go-result/result"
)

// Parser is used to verify or decrypt a token, and can be provided with
// a set of rules.
type Parser struct {
	rules []Rule
}

// NewParser returns a parser with NotExpired rule preloaded.
func NewParser() Parser {
	return Parser{[]Rule{NotExpired()}}
}

// NewParserWithoutExpiryCheck returns a parser with no currently set rules.
func NewParserWithoutExpiryCheck() Parser {
	return Parser{nil}
}

// NewParserForValidNow returns a parser that will require parsed tokens to be
// valid "now".
func NewParserForValidNow() Parser {
	return Parser{[]Rule{ValidAt(time.Now())}}
}

// MakeParser allows a parser to be constructed with a specified set of rules.
func MakeParser(rules []Rule) Parser {
	return Parser{rules}
}

// ParseV2Local will parse and decrypt a v2 local paseto and validate against
// any parser rules. Error if parsing, decryption, or any rule fails.
func (p Parser) ParseV2Local(key V2SymmetricKey, tainted string) (*Token, error) {
	msg := newMessage(V2Local, tainted)
	decrypted := result.FlatMap(msg, func(m message) result.Result[Token] {
		return m.v2Decrypt(key)
	})
	token := result.FlatMap(decrypted, p.validate)

	var t Token
	if err := token.Ok(&t); err != nil {
		return nil, err
	}

	return &t, nil
}

// ParseV2LocalAndConsume behaves like ParseV2Local and then atomically marks
// the token's "jti" as used in store. The store is accessed only after
// decryption and every parser rule succeeds.
func (p Parser) ParseV2LocalAndConsume(store ConsumptionStore, key V2SymmetricKey, tainted string) (*Token, error) {
	return p.parseAndConsume(func() result.Result[Token] {
		msg := newMessage(V2Local, tainted)
		return result.FlatMap(msg, func(m message) result.Result[Token] {
			return m.v2Decrypt(key)
		})
	}, store)
}

// ParseV2Public will parse and verify a v2 public paseto and validate against
// any parser rules. Error if parsing, verification, or any rule fails.
func (p Parser) ParseV2Public(key V2AsymmetricPublicKey, tainted string) (*Token, error) {
	msg := newMessage(V2Public, tainted)
	decrypted := result.FlatMap(msg, func(m message) result.Result[Token] {
		return m.v2Verify(key)
	})
	token := result.FlatMap(decrypted, p.validate)

	var t Token
	if err := token.Ok(&t); err != nil {
		return nil, err
	}

	return &t, nil
}

// ParseV2PublicAndConsume behaves like ParseV2Public and then atomically marks
// the token's "jti" as used in store. The store is accessed only after
// verification and every parser rule succeeds.
func (p Parser) ParseV2PublicAndConsume(store ConsumptionStore, key V2AsymmetricPublicKey, tainted string) (*Token, error) {
	return p.parseAndConsume(func() result.Result[Token] {
		msg := newMessage(V2Public, tainted)
		return result.FlatMap(msg, func(m message) result.Result[Token] {
			return m.v2Verify(key)
		})
	}, store)
}

// ParseV3Local will parse and decrypt a v3 local paseto and validate against
// any parser rules. Error if parsing, decryption, or any rule fails.
func (p Parser) ParseV3Local(key V3SymmetricKey, tainted string, implicit []byte) (*Token, error) {
	msg := newMessage(V3Local, tainted)
	decrypted := result.FlatMap(msg, func(m message) result.Result[Token] {
		return m.v3Decrypt(key, implicit)
	})
	token := result.FlatMap(decrypted, p.validate)

	var t Token
	if err := token.Ok(&t); err != nil {
		return nil, err
	}

	return &t, nil
}

// ParseV3LocalAndConsume behaves like ParseV3Local and then atomically marks
// the token's "jti" as used in store. The store is accessed only after
// decryption and every parser rule succeeds.
func (p Parser) ParseV3LocalAndConsume(store ConsumptionStore, key V3SymmetricKey, tainted string, implicit []byte) (*Token, error) {
	return p.parseAndConsume(func() result.Result[Token] {
		msg := newMessage(V3Local, tainted)
		return result.FlatMap(msg, func(m message) result.Result[Token] {
			return m.v3Decrypt(key, implicit)
		})
	}, store)
}

// ParseV3Public will parse and verify a v3 public paseto and validate against
// any parser rules. Error if parsing, verification, or any rule fails.
func (p Parser) ParseV3Public(key V3AsymmetricPublicKey, tainted string, implicit []byte) (*Token, error) {
	msg := newMessage(V3Public, tainted)
	decrypted := result.FlatMap(msg, func(m message) result.Result[Token] {
		return m.v3Verify(key, implicit)
	})
	token := result.FlatMap(decrypted, p.validate)

	var t Token
	if err := token.Ok(&t); err != nil {
		return nil, err
	}

	return &t, nil
}

// ParseV3PublicAndConsume behaves like ParseV3Public and then atomically marks
// the token's "jti" as used in store. The store is accessed only after
// verification and every parser rule succeeds.
func (p Parser) ParseV3PublicAndConsume(store ConsumptionStore, key V3AsymmetricPublicKey, tainted string, implicit []byte) (*Token, error) {
	return p.parseAndConsume(func() result.Result[Token] {
		msg := newMessage(V3Public, tainted)
		return result.FlatMap(msg, func(m message) result.Result[Token] {
			return m.v3Verify(key, implicit)
		})
	}, store)
}

// ParseV4Local will parse and decrypt a v4 local paseto and validate against
// any parser rules. Error if parsing, decryption, or any rule fails.
func (p Parser) ParseV4Local(key V4SymmetricKey, tainted string, implicit []byte) (*Token, error) {
	msg := newMessage(V4Local, tainted)
	decrypted := result.FlatMap(msg, func(m message) result.Result[Token] {
		return m.v4Decrypt(key, implicit)
	})
	token := result.FlatMap(decrypted, p.validate)

	var t Token
	if err := token.Ok(&t); err != nil {
		return nil, err
	}

	return &t, nil
}

// ParseV4LocalAndConsume behaves like ParseV4Local and then atomically marks
// the token's "jti" as used in store. The store is accessed only after
// decryption and every parser rule succeeds.
func (p Parser) ParseV4LocalAndConsume(store ConsumptionStore, key V4SymmetricKey, tainted string, implicit []byte) (*Token, error) {
	return p.parseAndConsume(func() result.Result[Token] {
		msg := newMessage(V4Local, tainted)
		return result.FlatMap(msg, func(m message) result.Result[Token] {
			return m.v4Decrypt(key, implicit)
		})
	}, store)
}

// ParseV4Public will parse and verify a v4 public paseto and validate against
// any parser rules. Error if parsing, verification, or any rule fails.
func (p Parser) ParseV4Public(key V4AsymmetricPublicKey, tainted string, implicit []byte) (*Token, error) {
	msg := newMessage(V4Public, tainted)
	decrypted := result.FlatMap(msg, func(m message) result.Result[Token] {
		return m.v4Verify(key, implicit)
	})
	token := result.FlatMap(decrypted, p.validate)

	var t Token
	if err := token.Ok(&t); err != nil {
		return nil, err
	}

	return &t, nil
}

// ParseV4PublicAndConsume behaves like ParseV4Public and then atomically marks
// the token's "jti" as used in store. The store is accessed only after
// verification and every parser rule succeeds.
func (p Parser) ParseV4PublicAndConsume(store ConsumptionStore, key V4AsymmetricPublicKey, tainted string, implicit []byte) (*Token, error) {
	return p.parseAndConsume(func() result.Result[Token] {
		msg := newMessage(V4Public, tainted)
		return result.FlatMap(msg, func(m message) result.Result[Token] {
			return m.v4Verify(key, implicit)
		})
	}, store)
}

// UnsafeParseFooter returns the footer of a Paseto message. Beware that this
// footer is not cryptographically verified at this stage, nor are any claims
// validated.
func (p Parser) UnsafeParseFooter(protocol Protocol, tainted string) ([]byte, error) {
	msg := newMessage(protocol, tainted)
	footer := result.Map(msg, message.unsafeFooter)

	var f []byte
	if err := footer.Ok(&f); err != nil {
		return nil, err
	}

	return f, nil
}

// SetRules will overwrite any currently set rules with those specified.
func (p *Parser) SetRules(rules []Rule) {
	p.rules = rules
}

// AddRule will add the given rule(s) to any already specified.
func (p *Parser) AddRule(rule ...Rule) {
	p.rules = append(p.rules, rule...)
}

func (p Parser) validate(token Token) result.Result[Token] {
	for _, rule := range p.rules {
		if err := rule(token); err != nil {
			return result.Err[Token](newRuleError(err))
		}
	}

	return result.Ok(token)
}
