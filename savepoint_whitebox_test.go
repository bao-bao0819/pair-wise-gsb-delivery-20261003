package bbolt

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Ensure that rolling back to a savepoint restores the transaction-scoped
// freelist state and the high water mark exactly, for both freelist
// backends.
func TestTx_Savepoint_FreelistRestore(t *testing.T) {
	for _, freelistType := range []FreelistType{FreelistArrayType, FreelistMapType} {
		t.Run(string(freelistType), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "db")
			db, err := Open(path, 0600, &Options{FreelistType: freelistType})
			require.NoError(t, err)
			defer db.Close()

			value := bytes.Repeat([]byte("v"), 256)

			// Seed two buckets and free the pages of the first one so that
			// the freelist has recyclable pages during the test. The second
			// one is deleted inside the test transaction to generate
			// pending free pages.
			require.NoError(t, db.Update(func(tx *Tx) error {
				for _, name := range []string{"seed", "seed2"} {
					b, err := tx.CreateBucket([]byte(name))
					require.NoError(t, err)
					for i := 0; i < 100; i++ {
						require.NoError(t, b.Put([]byte(fmt.Sprintf("k%03d", i)), value))
					}
				}
				return nil
			}))
			require.NoError(t, db.Update(func(tx *Tx) error {
				return tx.DeleteBucket([]byte("seed"))
			}))

			tx, err := db.Begin(true)
			require.NoError(t, err)
			defer tx.Rollback()

			b, err := tx.CreateBucket([]byte("data"))
			require.NoError(t, err)
			for i := 0; i < 50; i++ {
				require.NoError(t, b.Put([]byte(fmt.Sprintf("a%03d", i)), value))
			}

			require.NoError(t, tx.Savepoint("sp1"))

			// Capture the state that the savepoint must restore.
			freeCount := db.freelist.FreeCount()
			pendingCount := db.freelist.PendingCount()
			hwm := tx.meta.Pgid()

			// Free on-disk pages after the savepoint.
			for i := 0; i < 100; i++ {
				require.NoError(t, b.Put([]byte(fmt.Sprintf("b%03d", i)), value))
			}
			require.NoError(t, tx.DeleteBucket([]byte("seed2")))

			// Allocate pages after the savepoint, the same way the
			// commit-time spill does: recycled from the freelist and,
			// once it is exhausted, from the high water mark.
			_, err = tx.allocate(3)
			require.NoError(t, err)
			_, err = tx.allocate(2)
			require.NoError(t, err)

			// The mutations above must have changed the freelist state.
			require.Greater(t, db.freelist.PendingCount(), pendingCount)
			require.Less(t, db.freelist.FreeCount(), freeCount)

			require.NoError(t, tx.RollbackToSavepoint("sp1"))

			// The freelist and the high water mark are exactly restored.
			require.Equal(t, freeCount, db.freelist.FreeCount())
			require.Equal(t, pendingCount, db.freelist.PendingCount())
			require.Equal(t, hwm, tx.meta.Pgid())

			// The transaction is still fully usable afterwards.
			b = tx.Bucket([]byte("data"))
			for i := 0; i < 100; i++ {
				require.NoError(t, b.Put([]byte(fmt.Sprintf("c%03d", i)), value))
			}
			require.NoError(t, tx.Commit())

			// The database must be consistent: every page is either
			// reachable or on the freelist, and no page is freed twice.
			require.NoError(t, db.View(func(tx *Tx) error {
				for err := range tx.Check() {
					require.NoError(t, err)
				}
				return nil
			}))
		})
	}
}

// Ensure that savepoint snapshots survive mmap remapping: growing the
// database file while a savepoint is active must not corrupt the
// savepoint's snapshot.
func TestTx_Savepoint_MmapRemap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	db, err := Open(path, 0600, nil)
	require.NoError(t, err)
	defer db.Close()

	value := bytes.Repeat([]byte("v"), 1024)

	tx, err := db.Begin(true)
	require.NoError(t, err)
	defer tx.Rollback()

	b, err := tx.CreateBucket([]byte("data"))
	require.NoError(t, err)
	for i := 0; i < 100; i++ {
		require.NoError(t, b.Put([]byte(fmt.Sprintf("keep-%04d", i)), value))
	}

	require.NoError(t, tx.Savepoint("sp1"))

	// Force the mmap to grow while the savepoint is active.
	for i := 0; i < 2000; i++ {
		require.NoError(t, b.Put([]byte(fmt.Sprintf("drop-%04d", i)), value))
	}

	require.NoError(t, tx.RollbackToSavepoint("sp1"))

	// The snapshot must be intact after the remapping.
	b = tx.Bucket([]byte("data"))
	for i := 0; i < 100; i++ {
		require.Equal(t, value, b.Get([]byte(fmt.Sprintf("keep-%04d", i))))
	}
	require.Nil(t, b.Get([]byte("drop-0000")))

	require.NoError(t, tx.Commit())

	require.NoError(t, db.View(func(tx *Tx) error {
		for err := range tx.Check() {
			require.NoError(t, err)
		}
		return nil
	}))
}
