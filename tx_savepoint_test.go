package bbolt_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	bolt "go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"
	"go.etcd.io/bbolt/internal/btesting"
)

// Ensure that a transaction can be rolled back to a savepoint and still
// commit the changes made before the savepoint.
func TestTx_Savepoint_Basic(t *testing.T) {
	db := btesting.MustCreateDB(t)
	defer db.MustClose()

	err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucket([]byte("widgets"))
		require.NoError(t, err)
		require.NoError(t, b.Put([]byte("k1"), []byte("v1")))

		require.NoError(t, tx.Savepoint("sp1"))

		require.NoError(t, b.Put([]byte("k2"), []byte("v2")))
		require.NoError(t, b.Delete([]byte("k1")))

		require.NoError(t, tx.RollbackToSavepoint("sp1"))

		b = tx.Bucket([]byte("widgets"))
		require.Equal(t, []byte("v1"), b.Get([]byte("k1")))
		require.Nil(t, b.Get([]byte("k2")))

		require.NoError(t, b.Put([]byte("k3"), []byte("v3")))
		return nil
	})
	require.NoError(t, err)

	db.MustClose()
	db.MustReopen()

	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("widgets"))
		require.NotNil(t, b)
		require.Equal(t, []byte("v1"), b.Get([]byte("k1")))
		require.Nil(t, b.Get([]byte("k2")))
		require.Equal(t, []byte("v3"), b.Get([]byte("k3")))
		return nil
	})
	require.NoError(t, err)
}

// Ensure that multiple stacked savepoints can be rolled back level by
// level, that rolling back destroys newer savepoints, and that the final
// state is correctly persisted.
func TestTx_Savepoint_MultiLevel(t *testing.T) {
	db := btesting.MustCreateDB(t)
	defer db.MustClose()

	err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucket([]byte("widgets"))
		require.NoError(t, err)

		require.NoError(t, b.Put([]byte("k"), []byte("v0")))
		require.NoError(t, tx.Savepoint("sp1"))

		require.NoError(t, b.Put([]byte("k"), []byte("v1")))
		require.NoError(t, tx.Savepoint("sp2"))

		require.NoError(t, b.Put([]byte("k"), []byte("v2")))
		require.NoError(t, tx.Savepoint("sp3"))

		require.NoError(t, b.Put([]byte("k"), []byte("v3")))

		// Roll back one level: "v3" is discarded.
		require.NoError(t, tx.RollbackToSavepoint("sp3"))
		require.Equal(t, []byte("v2"), tx.Bucket([]byte("widgets")).Get([]byte("k")))

		// sp3 stays active after the rollback and can be rolled back to again.
		require.NoError(t, tx.Bucket([]byte("widgets")).Put([]byte("k"), []byte("v3-again")))
		require.NoError(t, tx.RollbackToSavepoint("sp3"))
		require.Equal(t, []byte("v2"), tx.Bucket([]byte("widgets")).Get([]byte("k")))

		// Roll back two levels: "v2" and "v1" are discarded, sp2 and sp3
		// are destroyed.
		require.NoError(t, tx.RollbackToSavepoint("sp1"))
		require.Equal(t, []byte("v0"), tx.Bucket([]byte("widgets")).Get([]byte("k")))
		require.ErrorIs(t, tx.RollbackToSavepoint("sp2"), berrors.ErrSavepointNotFound)
		require.ErrorIs(t, tx.RollbackToSavepoint("sp3"), berrors.ErrSavepointNotFound)

		// Re-modify after the rollback and commit.
		require.NoError(t, tx.Bucket([]byte("widgets")).Put([]byte("k"), []byte("final")))
		return nil
	})
	require.NoError(t, err)

	db.MustClose()
	db.MustReopen()

	err = db.View(func(tx *bolt.Tx) error {
		require.Equal(t, []byte("final"), tx.Bucket([]byte("widgets")).Get([]byte("k")))
		return nil
	})
	require.NoError(t, err)
	db.MustCheck()
}

// Ensure that nested buckets and bucket deletion/recreation are correctly
// rolled back to a savepoint.
func TestTx_Savepoint_NestedBuckets(t *testing.T) {
	db := btesting.MustCreateDB(t)
	defer db.MustClose()

	err := db.Update(func(tx *bolt.Tx) error {
		// Build a/b/c with one key at each level.
		a, err := tx.CreateBucket([]byte("a"))
		require.NoError(t, err)
		require.NoError(t, a.Put([]byte("ka"), []byte("va0")))
		b, err := a.CreateBucket([]byte("b"))
		require.NoError(t, err)
		require.NoError(t, b.Put([]byte("kb"), []byte("vb0")))
		c, err := b.CreateBucket([]byte("c"))
		require.NoError(t, err)
		require.NoError(t, c.Put([]byte("kc"), []byte("vc0")))

		require.NoError(t, tx.Savepoint("sp1"))

		// Modify all levels, then delete "b" (with its child "c") and
		// recreate it with different content.
		require.NoError(t, a.Put([]byte("ka"), []byte("va1")))
		require.NoError(t, a.DeleteBucket([]byte("b")))
		b2, err := a.CreateBucket([]byte("b"))
		require.NoError(t, err)
		require.NoError(t, b2.Put([]byte("kb-new"), []byte("vb-new")))

		// Sanity check the pre-rollback state.
		require.Equal(t, []byte("va1"), a.Get([]byte("ka")))
		require.Nil(t, a.Bucket([]byte("b")).Bucket([]byte("c")))
		require.Equal(t, []byte("vb-new"), a.Bucket([]byte("b")).Get([]byte("kb-new")))

		require.NoError(t, tx.RollbackToSavepoint("sp1"))

		// The whole nested structure is back to the savepoint state.
		a = tx.Bucket([]byte("a"))
		require.Equal(t, []byte("va0"), a.Get([]byte("ka")))
		b = a.Bucket([]byte("b"))
		require.NotNil(t, b)
		require.Equal(t, []byte("vb0"), b.Get([]byte("kb")))
		require.Nil(t, b.Get([]byte("kb-new")))
		c = b.Bucket([]byte("c"))
		require.NotNil(t, c)
		require.Equal(t, []byte("vc0"), c.Get([]byte("kc")))

		// Delete "b" again and roll back again: the savepoint stays active.
		require.NoError(t, a.DeleteBucket([]byte("b")))
		require.NoError(t, tx.RollbackToSavepoint("sp1"))
		require.NotNil(t, tx.Bucket([]byte("a")).Bucket([]byte("b")).Bucket([]byte("c")))
		return nil
	})
	require.NoError(t, err)

	db.MustClose()
	db.MustReopen()

	err = db.View(func(tx *bolt.Tx) error {
		a := tx.Bucket([]byte("a"))
		require.NotNil(t, a)
		require.Equal(t, []byte("va0"), a.Get([]byte("ka")))
		b := a.Bucket([]byte("b"))
		require.NotNil(t, b)
		require.Equal(t, []byte("vb0"), b.Get([]byte("kb")))
		c := b.Bucket([]byte("c"))
		require.NotNil(t, c)
		require.Equal(t, []byte("vc0"), c.Get([]byte("kc")))
		return nil
	})
	require.NoError(t, err)
	db.MustCheck()
}

// Ensure that bucket sequences are rolled back to a savepoint.
func TestTx_Savepoint_Sequence(t *testing.T) {
	db := btesting.MustCreateDB(t)
	defer db.MustClose()

	err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucket([]byte("widgets"))
		require.NoError(t, err)

		seq, err := b.NextSequence()
		require.NoError(t, err)
		require.Equal(t, uint64(1), seq)

		require.NoError(t, tx.Savepoint("sp1"))

		for i := 0; i < 5; i++ {
			_, err := b.NextSequence()
			require.NoError(t, err)
		}
		require.Equal(t, uint64(6), b.Sequence())

		require.NoError(t, tx.RollbackToSavepoint("sp1"))

		b = tx.Bucket([]byte("widgets"))
		require.Equal(t, uint64(1), b.Sequence())
		seq, err = b.NextSequence()
		require.NoError(t, err)
		require.Equal(t, uint64(2), seq)
		return nil
	})
	require.NoError(t, err)

	db.MustClose()
	db.MustReopen()

	err = db.View(func(tx *bolt.Tx) error {
		require.Equal(t, uint64(2), tx.Bucket([]byte("widgets")).Sequence())
		return nil
	})
	require.NoError(t, err)
}

// Ensure that page allocations and frees around the commit-time spill are
// correctly rolled back, and that the database and freelist stay
// consistent. The data volume forces node splits, page recycling from the
// freelist and high-water-mark growth (including mmap remapping while a
// savepoint is active).
func TestTx_Savepoint_SpillAndFreelist(t *testing.T) {
	db := btesting.MustCreateDB(t)
	defer db.MustClose()

	value := bytes.Repeat([]byte("v"), 512)
	key := func(prefix string, i int) []byte {
		return []byte(fmt.Sprintf("%s-%04d", prefix, i))
	}

	// Seed the database and then free its pages so that the freelist has
	// recyclable pages during the savepoint test.
	require.NoError(t, db.Fill([]byte("seed"), 2, 200,
		func(tx int, k int) []byte { return key("seed", tx*1000+k) },
		func(tx int, k int) []byte { return value },
	))
	require.NoError(t, db.Update(func(tx *bolt.Tx) error {
		return tx.DeleteBucket([]byte("seed"))
	}))

	err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucket([]byte("data"))
		require.NoError(t, err)

		// Phase 1 (before the savepoint): insert enough data to spill
		// onto multiple pages.
		for i := 0; i < 500; i++ {
			require.NoError(t, b.Put(key("keep", i), value))
		}

		require.NoError(t, tx.Savepoint("sp1"))

		// Phase 2 (after the savepoint): allocate many more pages, both
		// recycled from the freelist and fresh ones, and free pages by
		// deleting a bucket created after the savepoint.
		tmp, err := tx.CreateBucket([]byte("tmp"))
		require.NoError(t, err)
		for i := 0; i < 100; i++ {
			require.NoError(t, tmp.Put(key("tmp", i), value))
		}
		for i := 0; i < 500; i++ {
			require.NoError(t, b.Put(key("drop", i), value))
		}
		require.NoError(t, tx.DeleteBucket([]byte("tmp")))

		require.NoError(t, tx.RollbackToSavepoint("sp1"))

		// Everything from phase 2 is gone.
		require.Nil(t, tx.Bucket([]byte("tmp")))
		b = tx.Bucket([]byte("data"))
		require.Equal(t, value, b.Get(key("keep", 0)))
		require.Equal(t, value, b.Get(key("keep", 499)))
		require.Nil(t, b.Get(key("drop", 0)))

		// Phase 3: modify again after the rollback and commit.
		for i := 0; i < 300; i++ {
			require.NoError(t, b.Put(key("final", i), value))
		}
		return nil
	})
	require.NoError(t, err)

	db.MustClose()
	db.MustReopen()

	err = db.View(func(tx *bolt.Tx) error {
		require.Nil(t, tx.Bucket([]byte("tmp")))
		b := tx.Bucket([]byte("data"))
		require.NotNil(t, b)
		for _, prefix := range []string{"keep", "final"} {
			count := 0
			c := b.Cursor()
			for k, v := c.Seek(key(prefix, 0)); k != nil && bytes.HasPrefix(k, []byte(prefix)); k, v = c.Next() {
				require.Equal(t, value, v)
				count++
			}
			if prefix == "keep" {
				require.Equal(t, 500, count)
			} else {
				require.Equal(t, 300, count)
			}
		}
		require.Nil(t, b.Get(key("drop", 0)))
		return nil
	})
	require.NoError(t, err)

	// The consistency check verifies that every page is either reachable
	// or on the freelist, and that no page is freed twice.
	db.MustCheck()
}

// Ensure that OnCommit handlers registered after a savepoint are discarded
// when rolling back to it, while handlers registered before the savepoint
// are retained.
func TestTx_Savepoint_OnCommit(t *testing.T) {
	db := btesting.MustCreateDB(t)
	defer db.MustClose()

	var x int
	err := db.Update(func(tx *bolt.Tx) error {
		tx.OnCommit(func() { x += 1 })

		require.NoError(t, tx.Savepoint("sp1"))

		tx.OnCommit(func() { x += 10 })
		tx.OnCommit(func() { x += 100 })

		require.NoError(t, tx.RollbackToSavepoint("sp1"))

		tx.OnCommit(func() { x += 1000 })
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1001, x)
}

// Ensure that Bucket and Cursor handles obtained before a savepoint
// rollback are invalidated by the rollback.
func TestTx_Savepoint_StaleHandles(t *testing.T) {
	db := btesting.MustCreateDB(t)
	defer db.MustClose()

	err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucket([]byte("widgets"))
		require.NoError(t, err)
		require.NoError(t, b.Put([]byte("k1"), []byte("v1")))
		cursor := b.Cursor()
		require.NoError(t, tx.Savepoint("sp1"))
		require.NoError(t, b.Put([]byte("k2"), []byte("v2")))

		require.NoError(t, tx.RollbackToSavepoint("sp1"))

		// The old bucket handle is stale: error-returning methods report
		// ErrStaleHandle, the remaining methods panic.
		require.ErrorIs(t, b.Put([]byte("k3"), []byte("v3")), berrors.ErrStaleHandle)
		require.ErrorIs(t, b.Delete([]byte("k1")), berrors.ErrStaleHandle)
		require.ErrorIs(t, b.SetSequence(1), berrors.ErrStaleHandle)
		_, err = b.NextSequence()
		require.ErrorIs(t, err, berrors.ErrStaleHandle)
		_, err = b.CreateBucket([]byte("sub"))
		require.ErrorIs(t, err, berrors.ErrStaleHandle)
		require.ErrorIs(t, b.DeleteBucket([]byte("sub")), berrors.ErrStaleHandle)
		require.ErrorIs(t, b.ForEach(func(k, v []byte) error { return nil }), berrors.ErrStaleHandle)
		require.Panics(t, func() { b.Get([]byte("k1")) })
		require.Panics(t, func() { b.Bucket([]byte("sub")) })
		require.Panics(t, func() { b.Cursor() })
		require.Panics(t, func() { b.Sequence() })

		// The old cursor is stale as well.
		require.Panics(t, func() { cursor.First() })
		require.Panics(t, func() { cursor.Next() })
		require.ErrorIs(t, cursor.Delete(), berrors.ErrStaleHandle)

		// Fresh handles acquired after the rollback work fine.
		fresh := tx.Bucket([]byte("widgets"))
		require.Equal(t, []byte("v1"), fresh.Get([]byte("k1")))
		require.NoError(t, fresh.Put([]byte("k3"), []byte("v3")))
		k, v := fresh.Cursor().First()
		require.Equal(t, []byte("k1"), k)
		require.Equal(t, []byte("v1"), v)
		return nil
	})
	require.NoError(t, err)
}

// Ensure that releasing a savepoint destroys it and all savepoints created
// after it, while retaining the changes.
func TestTx_Savepoint_Release(t *testing.T) {
	db := btesting.MustCreateDB(t)
	defer db.MustClose()

	err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucket([]byte("widgets"))
		require.NoError(t, err)

		require.NoError(t, b.Put([]byte("k"), []byte("v0")))
		require.NoError(t, tx.Savepoint("sp1"))
		require.NoError(t, b.Put([]byte("k"), []byte("v1")))
		require.NoError(t, tx.Savepoint("sp2"))
		require.NoError(t, b.Put([]byte("k"), []byte("v2")))

		// Releasing sp1 destroys both sp1 and sp2.
		require.NoError(t, tx.ReleaseSavepoint("sp1"))
		require.ErrorIs(t, tx.RollbackToSavepoint("sp1"), berrors.ErrSavepointNotFound)
		require.ErrorIs(t, tx.RollbackToSavepoint("sp2"), berrors.ErrSavepointNotFound)
		require.ErrorIs(t, tx.ReleaseSavepoint("sp1"), berrors.ErrSavepointNotFound)

		// The changes are retained and committed.
		require.Equal(t, []byte("v2"), tx.Bucket([]byte("widgets")).Get([]byte("k")))
		return nil
	})
	require.NoError(t, err)

	db.MustClose()
	db.MustReopen()

	err = db.View(func(tx *bolt.Tx) error {
		require.Equal(t, []byte("v2"), tx.Bucket([]byte("widgets")).Get([]byte("k")))
		return nil
	})
	require.NoError(t, err)
}

// Ensure that savepoint misuse is reported with appropriate errors.
func TestTx_Savepoint_Errors(t *testing.T) {
	db := btesting.MustCreateDB(t)
	defer db.MustClose()

	// Savepoints are not allowed on read-only transactions.
	err := db.View(func(tx *bolt.Tx) error {
		require.ErrorIs(t, tx.Savepoint("sp"), berrors.ErrTxNotWritable)
		require.ErrorIs(t, tx.RollbackToSavepoint("sp"), berrors.ErrTxNotWritable)
		require.ErrorIs(t, tx.ReleaseSavepoint("sp"), berrors.ErrTxNotWritable)
		return nil
	})
	require.NoError(t, err)

	// Unknown savepoint names.
	err = db.Update(func(tx *bolt.Tx) error {
		require.ErrorIs(t, tx.RollbackToSavepoint("missing"), berrors.ErrSavepointNotFound)
		require.ErrorIs(t, tx.ReleaseSavepoint("missing"), berrors.ErrSavepointNotFound)
		return nil
	})
	require.NoError(t, err)

	// Savepoints are not allowed on a closed transaction.
	tx, err := db.Begin(true)
	require.NoError(t, err)
	require.NoError(t, tx.Savepoint("sp"))
	require.NoError(t, tx.Rollback())
	require.ErrorIs(t, tx.Savepoint("sp2"), berrors.ErrTxClosed)
	require.ErrorIs(t, tx.RollbackToSavepoint("sp"), berrors.ErrTxClosed)
	require.ErrorIs(t, tx.ReleaseSavepoint("sp"), berrors.ErrTxClosed)
}

// Ensure that a savepoint is not an independently committable nested
// transaction: rolling back the enclosing transaction discards everything,
// including changes made before any savepoint.
func TestTx_Savepoint_NotIndependentTransaction(t *testing.T) {
	db := btesting.MustCreateDB(t)
	defer db.MustClose()

	tx, err := db.Begin(true)
	require.NoError(t, err)

	b, err := tx.CreateBucket([]byte("widgets"))
	require.NoError(t, err)
	require.NoError(t, b.Put([]byte("k1"), []byte("v1")))
	require.NoError(t, tx.Savepoint("sp1"))
	require.NoError(t, b.Put([]byte("k2"), []byte("v2")))
	require.NoError(t, tx.RollbackToSavepoint("sp1"))

	// Rolling back the whole transaction discards even the changes made
	// before the savepoint.
	require.NoError(t, tx.Rollback())

	err = db.View(func(tx *bolt.Tx) error {
		require.Nil(t, tx.Bucket([]byte("widgets")))
		return nil
	})
	require.NoError(t, err)
}
