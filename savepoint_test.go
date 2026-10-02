package bbolt_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	bolt "go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"
	"go.etcd.io/bbolt/internal/btesting"
)

// runSavepointTestWithFreelistTypes runs the given test function against both
// freelist backend implementations.
func runSavepointTestWithFreelistTypes(t *testing.T, fn func(t *testing.T, db *btesting.DB)) {
	for _, ft := range []bolt.FreelistType{bolt.FreelistArrayType, bolt.FreelistMapType} {
		t.Run(string(ft), func(t *testing.T) {
			t.Setenv(btesting.TestFreelistType, string(ft))
			db := btesting.MustCreateDB(t)
			fn(t, db)
		})
	}
}

// TestTx_Savepoint_Basic ensures that changes made after a savepoint can be
// rolled back while earlier changes are kept and committed.
func TestTx_Savepoint_Basic(t *testing.T) {
	runSavepointTestWithFreelistTypes(t, func(t *testing.T, db *btesting.DB) {
		tx, err := db.Begin(true)
		require.NoError(t, err)

		b, err := tx.CreateBucket([]byte("widgets"))
		require.NoError(t, err)
		require.NoError(t, b.Put([]byte("k1"), []byte("v1")))

		require.NoError(t, tx.Savepoint("sp1"))
		require.NoError(t, b.Put([]byte("k2"), []byte("v2")))
		require.NoError(t, b.Delete([]byte("k1")))

		require.NoError(t, tx.RollbackToSavepoint("sp1"))

		// Handles were invalidated by the rollback; re-acquire the bucket.
		b = tx.Bucket([]byte("widgets"))
		require.NotNil(t, b)
		assert.Equal(t, []byte("v1"), b.Get([]byte("k1")))
		assert.Nil(t, b.Get([]byte("k2")))

		require.NoError(t, b.Put([]byte("k3"), []byte("v3")))
		require.NoError(t, tx.Commit())

		db.MustClose()
		db.MustReopen()

		err = db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("widgets"))
			require.NotNil(t, b)
			assert.Equal(t, []byte("v1"), b.Get([]byte("k1")))
			assert.Nil(t, b.Get([]byte("k2")))
			assert.Equal(t, []byte("v3"), b.Get([]byte("k3")))
			return nil
		})
		require.NoError(t, err)
	})
}

// TestTx_Savepoint_OnCommit ensures that commit handlers registered after a
// savepoint are discarded when rolling back to that savepoint, while earlier
// handlers are still executed on commit.
func TestTx_Savepoint_OnCommit(t *testing.T) {
	runSavepointTestWithFreelistTypes(t, func(t *testing.T, db *btesting.DB) {
		tx, err := db.Begin(true)
		require.NoError(t, err)

		b, err := tx.CreateBucket([]byte("widgets"))
		require.NoError(t, err)

		var handler1Called, handler2Called, handler3Called bool
		tx.OnCommit(func() { handler1Called = true })

		require.NoError(t, tx.Savepoint("sp1"))
		tx.OnCommit(func() { handler2Called = true })
		require.NoError(t, b.Put([]byte("k1"), []byte("v1")))

		require.NoError(t, tx.RollbackToSavepoint("sp1"))
		tx.OnCommit(func() { handler3Called = true })

		require.NoError(t, tx.Commit())

		assert.True(t, handler1Called, "handler registered before the savepoint must run")
		assert.False(t, handler2Called, "handler registered after the savepoint must be discarded")
		assert.True(t, handler3Called, "handler registered after the rollback must run")
	})
}

// TestTx_Savepoint_HandleInvalidation ensures that Bucket and Cursor handles
// obtained before a savepoint rollback are invalidated by the rollback and
// cannot be used to access rolled-back or reallocated pages.
func TestTx_Savepoint_HandleInvalidation(t *testing.T) {
	runSavepointTestWithFreelistTypes(t, func(t *testing.T, db *btesting.DB) {
		tx, err := db.Begin(true)
		require.NoError(t, err)

		b, err := tx.CreateBucket([]byte("widgets"))
		require.NoError(t, err)
		require.NoError(t, b.Put([]byte("k1"), []byte("v1")))
		c := b.Cursor()
		k, _ := c.First()
		require.Equal(t, []byte("k1"), k)

		require.NoError(t, tx.Savepoint("sp1"))
		require.NoError(t, b.Put([]byte("k2"), []byte("v2")))

		require.NoError(t, tx.RollbackToSavepoint("sp1"))

		// All handles obtained before the rollback are now invalid.
		assert.ErrorIs(t, b.Put([]byte("k3"), []byte("v3")), berrors.ErrSavepointRolledBack)
		assert.ErrorIs(t, b.Delete([]byte("k1")), berrors.ErrSavepointRolledBack)
		assert.ErrorIs(t, b.SetSequence(1), berrors.ErrSavepointRolledBack)
		_, err = b.NextSequence()
		assert.ErrorIs(t, err, berrors.ErrSavepointRolledBack)
		_, err = b.CreateBucket([]byte("sub"))
		assert.ErrorIs(t, err, berrors.ErrSavepointRolledBack)
		_, err = b.CreateBucketIfNotExists([]byte("sub"))
		assert.ErrorIs(t, err, berrors.ErrSavepointRolledBack)
		assert.ErrorIs(t, b.DeleteBucket([]byte("sub")), berrors.ErrSavepointRolledBack)
		assert.ErrorIs(t, b.ForEach(func(k, v []byte) error { return nil }), berrors.ErrSavepointRolledBack)
		assert.ErrorIs(t, c.Delete(), berrors.ErrSavepointRolledBack)

		// Methods which cannot return an error panic instead.
		assert.Panics(t, func() { b.Get([]byte("k1")) })
		assert.Panics(t, func() { b.Bucket([]byte("sub")) })
		assert.Panics(t, func() { b.Cursor() })
		assert.Panics(t, func() { b.Sequence() })
		assert.Panics(t, func() { b.Stats() })
		assert.Panics(t, func() { c.First() })
		assert.Panics(t, func() { c.Last() })
		assert.Panics(t, func() { c.Next() })
		assert.Panics(t, func() { c.Prev() })
		assert.Panics(t, func() { c.Seek([]byte("k1")) })

		// Re-acquired handles work as usual.
		b = tx.Bucket([]byte("widgets"))
		require.NotNil(t, b)
		assert.Equal(t, []byte("v1"), b.Get([]byte("k1")))
		assert.Nil(t, b.Get([]byte("k2")))
		require.NoError(t, b.Put([]byte("k3"), []byte("v3")))

		require.NoError(t, tx.Commit())

		err = db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("widgets"))
			require.NotNil(t, b)
			assert.Equal(t, []byte("v1"), b.Get([]byte("k1")))
			assert.Equal(t, []byte("v3"), b.Get([]byte("k3")))
			return nil
		})
		require.NoError(t, err)
	})
}

// TestTx_Savepoint_Spill ensures that savepoint rollback correctly restores
// the freelist and the page high-water mark when the transaction causes page
// splits, allocations and frees (i.e. activity that later spills onto pages),
// and that the database is consistent after committing and reopening.
func TestTx_Savepoint_Spill(t *testing.T) {
	runSavepointTestWithFreelistTypes(t, func(t *testing.T, db *btesting.DB) {
		tx, err := db.Begin(true)
		require.NoError(t, err)

		b, err := tx.CreateBucket([]byte("big"))
		require.NoError(t, err)

		// Insert enough data to force the bucket to span many pages.
		value := bytes.Repeat([]byte("v"), 100)
		for i := 0; i < 1000; i++ {
			require.NoError(t, b.Put([]byte(fmt.Sprintf("key-%06d", i)), value))
		}
		doomed, err := tx.CreateBucket([]byte("doomed"))
		require.NoError(t, err)
		for i := 0; i < 100; i++ {
			require.NoError(t, doomed.Put([]byte(fmt.Sprintf("key-%06d", i)), value))
		}

		require.NoError(t, tx.Savepoint("sp1"))

		// Free pages by deleting a bucket and half of the keys, and allocate
		// new pages (including overflow pages) by inserting more data.
		require.NoError(t, tx.DeleteBucket([]byte("doomed")))
		for i := 0; i < 1000; i += 2 {
			require.NoError(t, b.Delete([]byte(fmt.Sprintf("key-%06d", i))))
		}
		largeValue := bytes.Repeat([]byte("x"), 10000)
		for i := 1000; i < 1500; i++ {
			require.NoError(t, b.Put([]byte(fmt.Sprintf("key-%06d", i)), largeValue))
		}

		require.NoError(t, tx.RollbackToSavepoint("sp1"))

		// Verify the state is fully restored.
		b = tx.Bucket([]byte("big"))
		require.NotNil(t, b)
		for i := 0; i < 1000; i++ {
			v := b.Get([]byte(fmt.Sprintf("key-%06d", i)))
			require.Equal(t, value, v, "key-%06d", i)
		}
		require.NotNil(t, tx.Bucket([]byte("doomed")))

		// Modify again and commit; the commit spills the remaining state.
		for i := 1000; i < 1200; i++ {
			require.NoError(t, b.Put([]byte(fmt.Sprintf("key-%06d", i)), value))
		}
		require.NoError(t, tx.Commit())

		db.MustClose()
		db.MustReopen()

		err = db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("big"))
			require.NotNil(t, b)
			for i := 0; i < 1200; i++ {
				v := b.Get([]byte(fmt.Sprintf("key-%06d", i)))
				require.Equal(t, value, v, "key-%06d", i)
			}
			doomed := tx.Bucket([]byte("doomed"))
			require.NotNil(t, doomed)
			for i := 0; i < 100; i++ {
				v := doomed.Get([]byte(fmt.Sprintf("key-%06d", i)))
				require.Equal(t, value, v, "key-%06d", i)
			}
			return nil
		})
		require.NoError(t, err)

		// Verify the freelist is consistent with the committed content.
		db.MustCheck()
	})
}

// TestTx_Savepoint_TxRollback ensures that a savepoint is not an independent
// transaction: rolling back the whole transaction discards everything,
// including changes made before any savepoint.
func TestTx_Savepoint_TxRollback(t *testing.T) {
	runSavepointTestWithFreelistTypes(t, func(t *testing.T, db *btesting.DB) {
		tx, err := db.Begin(true)
		require.NoError(t, err)

		b, err := tx.CreateBucket([]byte("widgets"))
		require.NoError(t, err)
		require.NoError(t, b.Put([]byte("k1"), []byte("v1")))
		require.NoError(t, tx.Savepoint("sp1"))
		require.NoError(t, b.Put([]byte("k2"), []byte("v2")))

		require.NoError(t, tx.Rollback())

		err = db.View(func(tx *bolt.Tx) error {
			assert.Nil(t, tx.Bucket([]byte("widgets")))
			return nil
		})
		require.NoError(t, err)
	})
}

// TestTx_Savepoint_ErrorCases ensures that savepoint operations validate the
// transaction state and the savepoint names.
func TestTx_Savepoint_ErrorCases(t *testing.T) {
	runSavepointTestWithFreelistTypes(t, func(t *testing.T, db *btesting.DB) {
		// Savepoints are not allowed on read-only transactions.
		rtx, err := db.Begin(false)
		require.NoError(t, err)
		assert.ErrorIs(t, rtx.Savepoint("sp"), berrors.ErrTxNotWritable)
		assert.ErrorIs(t, rtx.RollbackToSavepoint("sp"), berrors.ErrTxNotWritable)
		assert.ErrorIs(t, rtx.ReleaseSavepoint("sp"), berrors.ErrTxNotWritable)
		require.NoError(t, rtx.Rollback())

		tx, err := db.Begin(true)
		require.NoError(t, err)

		// Duplicate savepoint names are rejected.
		require.NoError(t, tx.Savepoint("sp1"))
		assert.ErrorIs(t, tx.Savepoint("sp1"), berrors.ErrSavepointExists)

		// Unknown savepoint names are rejected.
		assert.ErrorIs(t, tx.RollbackToSavepoint("nope"), berrors.ErrSavepointNotFound)
		assert.ErrorIs(t, tx.ReleaseSavepoint("nope"), berrors.ErrSavepointNotFound)

		require.NoError(t, tx.Commit())

		// Savepoints are not allowed on a closed transaction.
		assert.ErrorIs(t, tx.Savepoint("sp2"), berrors.ErrTxClosed)
		assert.ErrorIs(t, tx.RollbackToSavepoint("sp1"), berrors.ErrTxClosed)
		assert.ErrorIs(t, tx.ReleaseSavepoint("sp1"), berrors.ErrTxClosed)
	})
}

// TestTx_Savepoint_MultiLevel ensures that nested savepoints can be rolled
// back level by level, that rolling back to an outer savepoint discards the
// inner ones, and that the final committed state (including the freelist) is
// consistent after reopening the database.
func TestTx_Savepoint_MultiLevel(t *testing.T) {
	runSavepointTestWithFreelistTypes(t, func(t *testing.T, db *btesting.DB) {
		tx, err := db.Begin(true)
		require.NoError(t, err)

		main, err := tx.CreateBucket([]byte("main"))
		require.NoError(t, err)
		require.NoError(t, main.Put([]byte("k1"), []byte("v1")))

		// Level 1.
		require.NoError(t, tx.Savepoint("sp1"))
		require.NoError(t, main.Put([]byte("k2"), []byte("v2")))
		child, err := main.CreateBucket([]byte("child"))
		require.NoError(t, err)
		require.NoError(t, child.Put([]byte("ck1"), []byte("cv1")))

		// Level 2.
		require.NoError(t, tx.Savepoint("sp2"))
		require.NoError(t, main.Put([]byte("k3"), []byte("v3")))
		require.NoError(t, main.Delete([]byte("k1")))
		require.NoError(t, child.Put([]byte("ck2"), []byte("cv2")))

		// Level 3.
		require.NoError(t, tx.Savepoint("sp3"))
		require.NoError(t, main.Put([]byte("k4"), []byte("v4")))
		require.NoError(t, main.DeleteBucket([]byte("child")))

		// Rolling back to sp3 undoes the child deletion and k4.
		require.NoError(t, tx.RollbackToSavepoint("sp3"))
		main = tx.Bucket([]byte("main"))
		child = main.Bucket([]byte("child"))
		require.NotNil(t, child)
		assert.Nil(t, main.Get([]byte("k4")))
		assert.Equal(t, []byte("cv2"), child.Get([]byte("ck2")))

		// The same savepoint can be rolled back to repeatedly.
		require.NoError(t, main.Put([]byte("k5"), []byte("v5")))
		require.NoError(t, tx.RollbackToSavepoint("sp3"))
		main = tx.Bucket([]byte("main"))
		assert.Nil(t, main.Get([]byte("k5")))

		// Rolling back to sp2 undoes k3 and the k1 deletion, and discards sp3.
		require.NoError(t, tx.RollbackToSavepoint("sp2"))
		assert.ErrorIs(t, tx.RollbackToSavepoint("sp3"), berrors.ErrSavepointNotFound)
		main = tx.Bucket([]byte("main"))
		child = main.Bucket([]byte("child"))
		require.NotNil(t, child)
		assert.Equal(t, []byte("v1"), main.Get([]byte("k1")))
		assert.Equal(t, []byte("v2"), main.Get([]byte("k2")))
		assert.Nil(t, main.Get([]byte("k3")))
		assert.Equal(t, []byte("cv1"), child.Get([]byte("ck1")))
		assert.Nil(t, child.Get([]byte("ck2")))

		// Modify again, then roll back to sp1: the child bucket disappears.
		require.NoError(t, main.Put([]byte("k6"), []byte("v6")))
		require.NoError(t, child.Put([]byte("ck9"), []byte("cv9")))
		require.NoError(t, tx.RollbackToSavepoint("sp1"))
		main = tx.Bucket([]byte("main"))
		assert.Equal(t, []byte("v1"), main.Get([]byte("k1")))
		assert.Nil(t, main.Get([]byte("k2")))
		assert.Nil(t, main.Get([]byte("k6")))
		assert.Nil(t, main.Bucket([]byte("child")))

		// Final modifications and commit.
		require.NoError(t, main.Put([]byte("k7"), []byte("v7")))
		require.NoError(t, tx.Commit())

		db.MustClose()
		db.MustReopen()

		err = db.View(func(tx *bolt.Tx) error {
			main := tx.Bucket([]byte("main"))
			require.NotNil(t, main)
			assert.Equal(t, []byte("v1"), main.Get([]byte("k1")))
			assert.Equal(t, []byte("v7"), main.Get([]byte("k7")))
			for _, k := range []string{"k2", "k3", "k4", "k5", "k6"} {
				assert.Nil(t, main.Get([]byte(k)), "key %s must not exist", k)
			}
			assert.Nil(t, main.Bucket([]byte("child")))
			return nil
		})
		require.NoError(t, err)

		// The consistency check verifies that every page is either reachable
		// or free, i.e. the freelist is consistent with the committed content.
		db.MustCheck()
	})
}

// TestTx_Savepoint_NestedBucket ensures that nested buckets created or
// deleted after a savepoint are correctly restored on rollback.
func TestTx_Savepoint_NestedBucket(t *testing.T) {
	runSavepointTestWithFreelistTypes(t, func(t *testing.T, db *btesting.DB) {
		tx, err := db.Begin(true)
		require.NoError(t, err)

		main, err := tx.CreateBucket([]byte("main"))
		require.NoError(t, err)
		child1, err := main.CreateBucket([]byte("child1"))
		require.NoError(t, err)
		require.NoError(t, child1.Put([]byte("ck1"), []byte("cv1")))
		grandChild, err := child1.CreateBucket([]byte("grand"))
		require.NoError(t, err)
		require.NoError(t, grandChild.Put([]byte("gk"), []byte("gv")))

		require.NoError(t, tx.Savepoint("sp1"))

		// Create a new nested bucket and modify the existing one.
		child2, err := main.CreateBucket([]byte("child2"))
		require.NoError(t, err)
		require.NoError(t, child2.Put([]byte("dk"), []byte("dv")))
		require.NoError(t, child1.Put([]byte("ck2"), []byte("cv2")))
		require.NoError(t, child1.DeleteBucket([]byte("grand")))

		require.NoError(t, tx.RollbackToSavepoint("sp1"))

		main = tx.Bucket([]byte("main"))
		assert.Nil(t, main.Bucket([]byte("child2")))
		child1 = main.Bucket([]byte("child1"))
		require.NotNil(t, child1)
		assert.Equal(t, []byte("cv1"), child1.Get([]byte("ck1")))
		assert.Nil(t, child1.Get([]byte("ck2")))
		grandChild = child1.Bucket([]byte("grand"))
		require.NotNil(t, grandChild)
		assert.Equal(t, []byte("gv"), grandChild.Get([]byte("gk")))

		require.NoError(t, tx.Commit())

		db.MustClose()
		db.MustReopen()

		err = db.View(func(tx *bolt.Tx) error {
			main := tx.Bucket([]byte("main"))
			require.NotNil(t, main)
			assert.Nil(t, main.Bucket([]byte("child2")))
			child1 := main.Bucket([]byte("child1"))
			require.NotNil(t, child1)
			assert.Equal(t, []byte("cv1"), child1.Get([]byte("ck1")))
			assert.Nil(t, child1.Get([]byte("ck2")))
			grandChild := child1.Bucket([]byte("grand"))
			require.NotNil(t, grandChild)
			assert.Equal(t, []byte("gv"), grandChild.Get([]byte("gk")))
			return nil
		})
		require.NoError(t, err)
		db.MustCheck()
	})
}

// TestTx_Savepoint_DeleteAndRecreateBucket ensures that a bucket which is
// deleted and recreated after a savepoint is restored to its original
// content when rolling back to the savepoint.
func TestTx_Savepoint_DeleteAndRecreateBucket(t *testing.T) {
	runSavepointTestWithFreelistTypes(t, func(t *testing.T, db *btesting.DB) {
		tx, err := db.Begin(true)
		require.NoError(t, err)

		b, err := tx.CreateBucket([]byte("bucket"))
		require.NoError(t, err)
		require.NoError(t, b.Put([]byte("k1"), []byte("v1")))
		require.NoError(t, b.Put([]byte("k2"), []byte("v2")))
		sub, err := b.CreateBucket([]byte("sub"))
		require.NoError(t, err)
		require.NoError(t, sub.Put([]byte("sk"), []byte("sv")))

		require.NoError(t, tx.Savepoint("sp1"))

		// Delete the bucket and recreate it with different content.
		require.NoError(t, tx.DeleteBucket([]byte("bucket")))
		b, err = tx.CreateBucket([]byte("bucket"))
		require.NoError(t, err)
		require.NoError(t, b.Put([]byte("k9"), []byte("v9")))

		require.NoError(t, tx.RollbackToSavepoint("sp1"))

		b = tx.Bucket([]byte("bucket"))
		require.NotNil(t, b)
		assert.Equal(t, []byte("v1"), b.Get([]byte("k1")))
		assert.Equal(t, []byte("v2"), b.Get([]byte("k2")))
		assert.Nil(t, b.Get([]byte("k9")))
		sub = b.Bucket([]byte("sub"))
		require.NotNil(t, sub)
		assert.Equal(t, []byte("sv"), sub.Get([]byte("sk")))

		require.NoError(t, tx.Commit())

		db.MustClose()
		db.MustReopen()

		err = db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("bucket"))
			require.NotNil(t, b)
			assert.Equal(t, []byte("v1"), b.Get([]byte("k1")))
			assert.Equal(t, []byte("v2"), b.Get([]byte("k2")))
			assert.Nil(t, b.Get([]byte("k9")))
			sub := b.Bucket([]byte("sub"))
			require.NotNil(t, sub)
			assert.Equal(t, []byte("sv"), sub.Get([]byte("sk")))
			return nil
		})
		require.NoError(t, err)
		db.MustCheck()
	})
}

// TestTx_Savepoint_Sequence ensures that bucket sequence numbers are
// restored when rolling back to a savepoint.
func TestTx_Savepoint_Sequence(t *testing.T) {
	runSavepointTestWithFreelistTypes(t, func(t *testing.T, db *btesting.DB) {
		tx, err := db.Begin(true)
		require.NoError(t, err)

		b, err := tx.CreateBucket([]byte("seq"))
		require.NoError(t, err)
		require.NoError(t, b.SetSequence(10))

		require.NoError(t, tx.Savepoint("sp1"))

		for i := uint64(11); i <= 13; i++ {
			seq, err := b.NextSequence()
			require.NoError(t, err)
			require.Equal(t, i, seq)
		}

		require.NoError(t, tx.RollbackToSavepoint("sp1"))

		b = tx.Bucket([]byte("seq"))
		assert.Equal(t, uint64(10), b.Sequence())
		seq, err := b.NextSequence()
		require.NoError(t, err)
		assert.Equal(t, uint64(11), seq)

		require.NoError(t, tx.Commit())

		db.MustClose()
		db.MustReopen()

		err = db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("seq"))
			require.NotNil(t, b)
			assert.Equal(t, uint64(11), b.Sequence())
			return nil
		})
		require.NoError(t, err)
	})
}

// TestTx_Savepoint_Release ensures that releasing a savepoint discards it
// and all savepoints created after it, without undoing any change.
func TestTx_Savepoint_Release(t *testing.T) {
	runSavepointTestWithFreelistTypes(t, func(t *testing.T, db *btesting.DB) {
		tx, err := db.Begin(true)
		require.NoError(t, err)

		b, err := tx.CreateBucket([]byte("widgets"))
		require.NoError(t, err)

		require.NoError(t, tx.Savepoint("a"))
		require.NoError(t, b.Put([]byte("k1"), []byte("v1")))
		require.NoError(t, tx.Savepoint("b"))
		require.NoError(t, b.Put([]byte("k2"), []byte("v2")))
		require.NoError(t, tx.Savepoint("c"))

		// Releasing "b" also discards "c", but keeps the changes.
		require.NoError(t, tx.ReleaseSavepoint("b"))
		assert.ErrorIs(t, tx.RollbackToSavepoint("c"), berrors.ErrSavepointNotFound)
		assert.ErrorIs(t, tx.RollbackToSavepoint("b"), berrors.ErrSavepointNotFound)

		// "a" is still active and rolls back both k1 and k2.
		require.NoError(t, tx.RollbackToSavepoint("a"))
		b = tx.Bucket([]byte("widgets"))
		assert.Nil(t, b.Get([]byte("k1")))
		assert.Nil(t, b.Get([]byte("k2")))

		// Releasing "a" leaves no active savepoint behind.
		require.NoError(t, tx.ReleaseSavepoint("a"))
		assert.ErrorIs(t, tx.RollbackToSavepoint("a"), berrors.ErrSavepointNotFound)

		require.NoError(t, b.Put([]byte("k3"), []byte("v3")))
		require.NoError(t, tx.Commit())

		err = db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("widgets"))
			require.NotNil(t, b)
			assert.Equal(t, []byte("v3"), b.Get([]byte("k3")))
			return nil
		})
		require.NoError(t, err)
	})
}
