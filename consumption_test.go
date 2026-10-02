package paseto_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"aidanwoods.dev/go-paseto"
	"github.com/stretchr/testify/require"
)

type consumptionCall struct {
	jti       string
	expiresAt time.Time
}

type fakeConsumptionStore struct {
	mu    sync.Mutex
	used  map[string]struct{}
	calls []consumptionCall
	err   error
}

func newFakeConsumptionStore() *fakeConsumptionStore {
	return &fakeConsumptionStore{used: make(map[string]struct{})}
}

func (s *fakeConsumptionStore) ConsumeJTI(jti string, expiresAt time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls = append(s.calls, consumptionCall{jti: jti, expiresAt: expiresAt})
	if s.err != nil {
		return false, s.err
	}

	if _, ok := s.used[jti]; ok {
		return false, nil
	}

	s.used[jti] = struct{}{}
	return true, nil
}

func (s *fakeConsumptionStore) snapshot() []consumptionCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	calls := make([]consumptionCall, len(s.calls))
	copy(calls, s.calls)
	return calls
}

func newConsumableToken(jti string, expiresAt time.Time) paseto.Token {
	token := paseto.NewToken()
	token.SetJti(jti)
	token.SetExpiration(expiresAt)
	return token
}

func TestParseAndConsumeSupportsAllProtocols(t *testing.T) {
	expiresAt := time.Now().Add(time.Minute)

	v2SymmetricKey := paseto.NewV2SymmetricKey()
	v2SecretKey := paseto.NewV2AsymmetricSecretKey()
	v3SymmetricKey := paseto.NewV3SymmetricKey()
	v3SecretKey := paseto.NewV3AsymmetricSecretKey()
	v4SymmetricKey := paseto.NewV4SymmetricKey()
	v4SecretKey := paseto.NewV4AsymmetricSecretKey()

	tests := []struct {
		name    string
		jti     string
		consume func(paseto.ConsumptionStore) (*paseto.Token, error)
	}{
		{
			name: "v2 local",
			jti:  "jti-v2-local",
			consume: func(store paseto.ConsumptionStore) (*paseto.Token, error) {
				token := newConsumableToken("jti-v2-local", expiresAt)
				return paseto.NewParser().ParseV2LocalAndConsume(store, v2SymmetricKey, token.V2Encrypt(v2SymmetricKey))
			},
		},
		{
			name: "v2 public",
			jti:  "jti-v2-public",
			consume: func(store paseto.ConsumptionStore) (*paseto.Token, error) {
				token := newConsumableToken("jti-v2-public", expiresAt)
				return paseto.NewParser().ParseV2PublicAndConsume(store, v2SecretKey.Public(), token.V2Sign(v2SecretKey))
			},
		},
		{
			name: "v3 local",
			jti:  "jti-v3-local",
			consume: func(store paseto.ConsumptionStore) (*paseto.Token, error) {
				token := newConsumableToken("jti-v3-local", expiresAt)
				return paseto.NewParser().ParseV3LocalAndConsume(store, v3SymmetricKey, token.V3Encrypt(v3SymmetricKey, nil), nil)
			},
		},
		{
			name: "v3 public",
			jti:  "jti-v3-public",
			consume: func(store paseto.ConsumptionStore) (*paseto.Token, error) {
				token := newConsumableToken("jti-v3-public", expiresAt)
				return paseto.NewParser().ParseV3PublicAndConsume(store, v3SecretKey.Public(), token.V3Sign(v3SecretKey, nil), nil)
			},
		},
		{
			name: "v4 local",
			jti:  "jti-v4-local",
			consume: func(store paseto.ConsumptionStore) (*paseto.Token, error) {
				token := newConsumableToken("jti-v4-local", expiresAt)
				return paseto.NewParser().ParseV4LocalAndConsume(store, v4SymmetricKey, token.V4Encrypt(v4SymmetricKey, nil), nil)
			},
		},
		{
			name: "v4 public",
			jti:  "jti-v4-public",
			consume: func(store paseto.ConsumptionStore) (*paseto.Token, error) {
				token := newConsumableToken("jti-v4-public", expiresAt)
				return paseto.NewParser().ParseV4PublicAndConsume(store, v4SecretKey.Public(), token.V4Sign(v4SecretKey, nil), nil)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newFakeConsumptionStore()

			parsedToken, err := test.consume(store)
			require.NoError(t, err)
			require.NotNil(t, parsedToken)

			calls := store.snapshot()
			require.Len(t, calls, 1)
			require.Equal(t, test.jti, calls[0].jti)
			require.WithinDuration(t, expiresAt, calls[0].expiresAt, time.Second)
		})
	}
}

func TestParseAndConsumeConcurrentJTIOnlyOneSucceeds(t *testing.T) {
	store := newFakeConsumptionStore()
	key := paseto.NewV4AsymmetricSecretKey()
	token := newConsumableToken("concurrent-jti", time.Now().Add(time.Minute))
	signed := token.V4Sign(key, nil)

	const requestCount = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, requestCount)

	for range requestCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := paseto.NewParser().ParseV4PublicAndConsume(store, key.Public(), signed, nil)
			errs <- err
		}()
	}

	close(start)
	wg.Wait()
	close(errs)

	successes := 0
	reuses := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, paseto.ErrJTIAlreadyConsumed):
			reuses++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}

	require.Equal(t, 1, successes)
	require.Equal(t, requestCount-1, reuses)
	require.Len(t, store.snapshot(), requestCount)
}

func TestParseAndConsumeDistinguishesStorageErrorFromReuse(t *testing.T) {
	key := paseto.NewV4AsymmetricSecretKey()
	token := newConsumableToken("storage-error-jti", time.Now().Add(time.Minute))
	signed := token.V4Sign(key, nil)
	storeError := errors.New("storage unavailable")

	store := newFakeConsumptionStore()
	store.err = storeError
	_, err := paseto.NewParser().ParseV4PublicAndConsume(store, key.Public(), signed, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, storeError)
	var typedStoreError paseto.ConsumptionStoreError
	require.True(t, errors.As(err, &typedStoreError))
	require.NotErrorIs(t, err, paseto.ErrJTIAlreadyConsumed)

	duplicateStore := newFakeConsumptionStore()
	duplicateStore.used["duplicate-jti"] = struct{}{}
	duplicateToken := newConsumableToken("duplicate-jti", time.Now().Add(time.Minute))
	duplicateSigned := duplicateToken.V4Sign(key, nil)
	_, err = paseto.NewParser().ParseV4PublicAndConsume(duplicateStore, key.Public(), duplicateSigned, nil)
	require.ErrorIs(t, err, paseto.ErrJTIAlreadyConsumed)

	var consumptionStoreError paseto.ConsumptionStoreError
	require.False(t, errors.As(err, &consumptionStoreError))
}

func TestParseAndConsumeDoesNotTouchStoreBeforeRulesPass(t *testing.T) {
	key := paseto.NewV4AsymmetricSecretKey()
	store := newFakeConsumptionStore()
	events := make([]string, 0)

	parser := paseto.NewParser()
	parser.AddRule(ruleEvent("rule-one", &events, false))
	parser.AddRule(ruleEvent("rule-two", &events, true))

	token := newConsumableToken("failed-rule-jti", time.Now().Add(time.Minute))
	_, err := parser.ParseV4PublicAndConsume(store, key.Public(), token.V4Sign(key, nil), nil)
	require.Error(t, err)
	require.ErrorIs(t, err, &paseto.RuleError{})
	require.Equal(t, []string{"rule-one", "rule-two"}, events)
	require.Empty(t, store.snapshot())
}

func TestParseAndConsumeDoesNotTouchStoreForInvalidCryptography(t *testing.T) {
	store := newFakeConsumptionStore()

	_, err := paseto.NewParser().ParseV4PublicAndConsume(
		store,
		paseto.NewV4AsymmetricSecretKey().Public(),
		"v4.public.invalid",
		nil,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, &paseto.TokenError{})
	require.Empty(t, store.snapshot())
}

func TestParseAndConsumeRequiresJTIAndExpiration(t *testing.T) {
	key := paseto.NewV4AsymmetricSecretKey()

	t.Run("missing jti", func(t *testing.T) {
		store := newFakeConsumptionStore()
		token := paseto.NewToken()
		token.SetExpiration(time.Now().Add(time.Minute))

		_, err := paseto.NewParser().ParseV4PublicAndConsume(store, key.Public(), token.V4Sign(key, nil), nil)
		require.ErrorIs(t, err, &paseto.RuleError{})
		require.Empty(t, store.snapshot())
	})

	t.Run("empty jti", func(t *testing.T) {
		store := newFakeConsumptionStore()
		token := newConsumableToken("", time.Now().Add(time.Minute))

		_, err := paseto.NewParser().ParseV4PublicAndConsume(store, key.Public(), token.V4Sign(key, nil), nil)
		require.ErrorIs(t, err, &paseto.RuleError{})
		require.Empty(t, store.snapshot())
	})

	t.Run("missing expiration", func(t *testing.T) {
		store := newFakeConsumptionStore()
		token := paseto.NewToken()
		token.SetJti("missing-expiration-jti")

		_, err := paseto.NewParserWithoutExpiryCheck().ParseV4PublicAndConsume(store, key.Public(), token.V4Sign(key, nil), nil)
		require.ErrorIs(t, err, &paseto.RuleError{})
		require.Empty(t, store.snapshot())
	})

	t.Run("invalid expiration", func(t *testing.T) {
		store := newFakeConsumptionStore()
		token := paseto.NewToken()
		token.SetJti("invalid-expiration-jti")
		require.NoError(t, token.Set("exp", "not a time"))

		_, err := paseto.NewParserWithoutExpiryCheck().ParseV4PublicAndConsume(store, key.Public(), token.V4Sign(key, nil), nil)
		require.ErrorIs(t, err, &paseto.RuleError{})
		require.Empty(t, store.snapshot())
	})

	t.Run("expired even without expiry rule", func(t *testing.T) {
		store := newFakeConsumptionStore()
		token := newConsumableToken("expired-jti", time.Now().Add(-time.Minute))

		_, err := paseto.NewParserWithoutExpiryCheck().ParseV4PublicAndConsume(store, key.Public(), token.V4Sign(key, nil), nil)
		require.ErrorIs(t, err, &paseto.RuleError{})
		require.Empty(t, store.snapshot())
	})
}

func TestParseAndConsumeTouchesStoreAfterAllRules(t *testing.T) {
	key := paseto.NewV4AsymmetricSecretKey()
	store := newFakeConsumptionStore()
	events := make([]string, 0)

	parser := paseto.NewParser()
	parser.AddRule(ruleEvent("rule-one", &events, false))
	parser.AddRule(ruleEvent("rule-two", &events, false))

	token := newConsumableToken("ordered-jti", time.Now().Add(time.Minute))
	_, err := parser.ParseV4PublicAndConsume(store, key.Public(), token.V4Sign(key, nil), nil)
	require.NoError(t, err)
	require.Equal(t, []string{"rule-one", "rule-two"}, events)
	require.Len(t, store.snapshot(), 1)
}

func TestParseRemainsSideEffectFree(t *testing.T) {
	key := paseto.NewV4AsymmetricSecretKey()
	token := newConsumableToken("parse-only-jti", time.Now().Add(time.Minute))
	signed := token.V4Sign(key, nil)
	store := newFakeConsumptionStore()

	parsedToken, err := paseto.NewParser().ParseV4Public(key.Public(), signed, nil)
	require.NoError(t, err)
	require.NotNil(t, parsedToken)
	require.Empty(t, store.snapshot())
}

func TestConsumptionIsNotRolledBackWhenCallerDoesNotContinue(t *testing.T) {
	key := paseto.NewV4AsymmetricSecretKey()
	token := newConsumableToken("committed-jti", time.Now().Add(time.Minute))
	signed := token.V4Sign(key, nil)
	store := newFakeConsumptionStore()
	parser := paseto.NewParser()

	_, err := parser.ParseV4PublicAndConsume(store, key.Public(), signed, nil)
	require.NoError(t, err)

	_, err = parser.ParseV4PublicAndConsume(store, key.Public(), signed, nil)
	require.ErrorIs(t, err, paseto.ErrJTIAlreadyConsumed)
}

func ruleEvent(name string, events *[]string, fail bool) paseto.Rule {
	return func(paseto.Token) error {
		*events = append(*events, name)
		if fail {
			return errors.New(name + " failed")
		}
		return nil
	}
}
