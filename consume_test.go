package paseto_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"aidanwoods.dev/go-paseto"
	"github.com/stretchr/testify/require"
)

var errStorageDown = errors.New("storage is down")

// fakeConsumedStore is an in-memory ConsumedTokenStore. It records every
// MarkConsumed call (in order, relative to recorded rule evaluations) so the
// sequencing between rules and the store write can be asserted.
type fakeConsumedStore struct {
	mu       sync.Mutex
	consumed map[string]bool
	events   []string
	failWith error
}

func newFakeConsumedStore() *fakeConsumedStore {
	return &fakeConsumedStore{consumed: make(map[string]bool)}
}

func (s *fakeConsumedStore) MarkConsumed(jti string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.events = append(s.events, "mark:"+jti)

	if s.failWith != nil {
		return s.failWith
	}

	if s.consumed[jti] {
		return paseto.ErrTokenAlreadyConsumed
	}

	s.consumed[jti] = true

	return nil
}

func (s *fakeConsumedStore) snapshotEvents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	events := make([]string, len(s.events))
	copy(events, s.events)

	return events
}

func (s *fakeConsumedStore) markedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.consumed)
}

// recordingRule appends an event each time it runs, so tests can assert that
// rules run before (and failing rules run instead of) the store write.
func recordingRule(store *fakeConsumedStore, label string) paseto.Rule {
	return func(paseto.Token) error {
		store.mu.Lock()
		defer store.mu.Unlock()

		store.events = append(store.events, "rule:"+label)

		return nil
	}
}

func encryptedOneTimeToken(t *testing.T) (string, paseto.V4SymmetricKey) {
	t.Helper()

	key := paseto.NewV4SymmetricKey()
	token := paseto.NewToken()
	token.SetJti("jti-1")
	token.SetExpiration(time.Now().Add(time.Hour))

	return token.V4Encrypt(key, nil), key
}

func signedOneTimeToken(t *testing.T) (string, paseto.V4AsymmetricPublicKey) {
	t.Helper()

	secretKey := paseto.NewV4AsymmetricSecretKey()
	token := paseto.NewToken()
	token.SetJti("jti-public-1")
	token.SetExpiration(time.Now().Add(time.Hour))

	return token.V4Sign(secretKey, nil), secretKey.Public()
}

func TestConsumingParser_MarksAfterRules(t *testing.T) {
	store := newFakeConsumedStore()
	signed, key := encryptedOneTimeToken(t)

	parser := paseto.NewConsumingParser(store)
	parser.AddRule(recordingRule(store, "custom"))

	token, err := parser.ParseV4Local(key, signed, nil)
	require.NoError(t, err)
	require.NotNil(t, token)

	gotJti, err := token.GetJti()
	require.NoError(t, err)
	require.Equal(t, "jti-1", gotJti)

	// The rule must evaluate before the atomic store write.
	require.Equal(t, []string{"rule:custom", "mark:jti-1"}, store.snapshotEvents())
	require.Equal(t, 1, store.markedCount())
}

func TestConsumingParser_ReplayRejected(t *testing.T) {
	store := newFakeConsumedStore()
	signed, key := encryptedOneTimeToken(t)
	parser := paseto.NewConsumingParser(store)

	_, err := parser.ParseV4Local(key, signed, nil)
	require.NoError(t, err)

	_, err = parser.ParseV4Local(key, signed, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, paseto.ErrTokenAlreadyConsumed)
	require.ErrorIs(t, err, paseto.RuleError{})

	var storeErr paseto.StoreError
	require.False(t, errors.As(err, &storeErr))

	require.Equal(t, 1, store.markedCount())
}

func TestConsumingParser_PublicVariantReplayRejected(t *testing.T) {
	store := newFakeConsumedStore()
	signed, publicKey := signedOneTimeToken(t)
	parser := paseto.NewConsumingParser(store)

	_, err := parser.ParseV4Public(publicKey, signed, nil)
	require.NoError(t, err)

	_, err = parser.ParseV4Public(publicKey, signed, nil)
	require.ErrorIs(t, err, paseto.ErrTokenAlreadyConsumed)
	require.Equal(t, 1, store.markedCount())
}

func TestConsumingParser_StorageErrorIsDistinct(t *testing.T) {
	store := newFakeConsumedStore()
	store.failWith = errStorageDown
	signed, key := encryptedOneTimeToken(t)
	parser := paseto.NewConsumingParser(store)

	_, err := parser.ParseV4Local(key, signed, nil)
	require.Error(t, err)

	var storeErr paseto.StoreError
	require.ErrorAs(t, err, &storeErr)
	require.ErrorIs(t, err, errStorageDown)

	// A storage failure is not reported as a replay.
	require.False(t, errors.Is(err, paseto.ErrTokenAlreadyConsumed))

	// Nothing was confirmed consumed: a retry against a healthy store wins.
	store.failWith = nil

	token, err := parser.ParseV4Local(key, signed, nil)
	require.NoError(t, err)
	require.NotNil(t, token)
	require.Equal(t, 1, store.markedCount())
}

func TestConsumingParser_MissingJtiNotStored(t *testing.T) {
	store := newFakeConsumedStore()
	key := paseto.NewV4SymmetricKey()

	token := paseto.NewToken()
	token.SetExpiration(time.Now().Add(time.Hour))

	parser := paseto.NewConsumingParser(store)
	_, err := parser.ParseV4Local(key, token.V4Encrypt(key, nil), nil)
	require.ErrorIs(t, err, paseto.ErrJtiMissing)
	require.ErrorIs(t, err, paseto.RuleError{})
	require.Empty(t, store.snapshotEvents())
	require.Zero(t, store.markedCount())
}

func TestConsumingParser_EmptyJtiNotStored(t *testing.T) {
	store := newFakeConsumedStore()
	key := paseto.NewV4SymmetricKey()

	token := paseto.NewToken()
	token.SetJti("")
	token.SetExpiration(time.Now().Add(time.Hour))

	parser := paseto.NewConsumingParser(store)
	_, err := parser.ParseV4Local(key, token.V4Encrypt(key, nil), nil)
	require.ErrorIs(t, err, paseto.ErrJtiMissing)
	require.Empty(t, store.snapshotEvents())
}

func TestConsumingParser_MissingExpirationNotStored(t *testing.T) {
	store := newFakeConsumedStore()
	key := paseto.NewV4SymmetricKey()

	token := paseto.NewToken()
	token.SetJti("jti-no-exp")

	// Bypass the preloaded NotExpired rule so the consumption precondition is
	// what surfaces, rather than the generic expiry rule error.
	parser := paseto.NewConsumingParserWithoutExpiryCheck(store)
	_, err := parser.ParseV4Local(key, token.V4Encrypt(key, nil), nil)
	require.ErrorIs(t, err, paseto.ErrExpirationMissing)
	require.ErrorIs(t, err, paseto.RuleError{})
	require.Empty(t, store.snapshotEvents())
}

func TestConsumingParser_InvalidTimeNotStored(t *testing.T) {
	store := newFakeConsumedStore()
	key := paseto.NewV4SymmetricKey()

	// "exp" is present but is a number rather than an RFC3339 string.
	token, err := paseto.NewTokenFromClaimsJSON(
		[]byte(`{"jti":"jti-bad-time","exp":123}`),
		nil,
	)
	require.NoError(t, err)

	parser := paseto.NewConsumingParserWithoutExpiryCheck(store)
	_, err = parser.ParseV4Local(key, token.V4Encrypt(key, nil), nil)
	require.ErrorIs(t, err, paseto.ErrInvalidTime)
	require.ErrorIs(t, err, paseto.RuleError{})
	require.Empty(t, store.snapshotEvents())
}

func TestConsumingParser_ExpiredTokenNotStored(t *testing.T) {
	store := newFakeConsumedStore()
	key := paseto.NewV4SymmetricKey()

	token := paseto.NewToken()
	token.SetJti("jti-expired")
	token.SetExpiration(time.Now().Add(-time.Hour))

	parser := paseto.NewConsumingParser(store)
	_, err := parser.ParseV4Local(key, token.V4Encrypt(key, nil), nil)
	require.Error(t, err)
	require.ErrorIs(t, err, paseto.RuleError{})
	require.NotErrorIs(t, err, paseto.ErrTokenAlreadyConsumed)
	require.Empty(t, store.snapshotEvents())
}

func TestConsumingParser_FailingRuleNotStored(t *testing.T) {
	store := newFakeConsumedStore()
	signed, key := encryptedOneTimeToken(t)

	parser := paseto.NewConsumingParser(store)
	parser.AddRule(recordingRule(store, "ok"))
	parser.AddRule(func(paseto.Token) error {
		return errors.New("boom")
	})

	_, err := parser.ParseV4Local(key, signed, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, paseto.RuleError{})

	// First rule ran; the failing rule short-circuited before any store write.
	require.Equal(t, []string{"rule:ok"}, store.snapshotEvents())
	require.Zero(t, store.markedCount())
}

func TestConsumingParser_DecryptionFailureNotStored(t *testing.T) {
	store := newFakeConsumedStore()
	signed, _ := encryptedOneTimeToken(t)

	parser := paseto.NewConsumingParser(store)

	// Decrypt with a different key: the token is never authenticated.
	_, err := parser.ParseV4Local(paseto.NewV4SymmetricKey(), signed, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, paseto.TokenError{})
	require.NotErrorIs(t, err, paseto.RuleError{})
	require.Empty(t, store.snapshotEvents())
}

func TestConsumingParser_ConcurrentSingleWinner(t *testing.T) {
	store := newFakeConsumedStore()
	signed, key := encryptedOneTimeToken(t)

	const goroutines = 64

	parser := paseto.NewConsumingParser(store)

	start := make(chan struct{})
	var wg sync.WaitGroup

	var successes, replays, other int

	var countersMu sync.Mutex

	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start

			_, err := parser.ParseV4Local(key, signed, nil)

			countersMu.Lock()
			defer countersMu.Unlock()

			switch {
			case err == nil:
				successes++
			case errors.Is(err, paseto.ErrTokenAlreadyConsumed):
				replays++
			default:
				other++
			}
		}()
	}

	close(start)
	wg.Wait()

	require.Equal(t, 1, successes, "exactly one concurrent request must win")
	require.Equal(t, goroutines-1, replays)
	require.Zero(t, other)
	require.Equal(t, 1, store.markedCount())
}

func TestPlainParser_HasNoStoreSideEffect(t *testing.T) {
	store := newFakeConsumedStore()
	signed, key := encryptedOneTimeToken(t)

	// A normal Parser never receives a store and must not touch one.
	parser := paseto.NewParser()

	token, err := parser.ParseV4Local(key, signed, nil)
	require.NoError(t, err)
	require.NotNil(t, token)
	require.Empty(t, store.snapshotEvents())
	require.Zero(t, store.markedCount())

	// The ordinary parse leaves the token replayable via the consuming path.
	consuming := paseto.NewConsumingParser(store)

	first, err := consuming.ParseV4Local(key, signed, nil)
	require.NoError(t, err)
	require.NotNil(t, first)

	_, err = consuming.ParseV4Local(key, signed, nil)
	require.ErrorIs(t, err, paseto.ErrTokenAlreadyConsumed)
}

func TestConsumingParser_MakeConsumingParser(t *testing.T) {
	store := newFakeConsumedStore()
	signed, key := encryptedOneTimeToken(t)

	// No expiry rule supplied: consumption still requires a valid jti and exp.
	parser := paseto.MakeConsumingParser(nil, store)

	token, err := parser.ParseV4Local(key, signed, nil)
	require.NoError(t, err)
	require.NotNil(t, token)
	require.Equal(t, []string{"mark:jti-1"}, store.snapshotEvents())
}
