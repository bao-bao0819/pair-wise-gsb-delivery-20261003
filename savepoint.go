package bbolt

import (
	"maps"
	"slices"

	berrors "go.etcd.io/bbolt/errors"
	"go.etcd.io/bbolt/internal/common"
	"go.etcd.io/bbolt/internal/freelist"
)

// savepoint is a named snapshot of the complete in-memory state of a
// writable transaction. Since a writable transaction only ever writes to
// disk during Commit, restoring a savepoint is a purely in-memory
// operation: the bucket/node tree, the dirty page cache, the meta page
// (including the high water mark) and the transaction-scoped freelist
// mutations are all restored to the state captured by the savepoint.
type savepoint struct {
	name string

	meta              common.Meta                  // meta copy (root bucket, high water mark, etc.)
	root              *Bucket                      // deep clone of the bucket/node tree
	pages             map[common.Pgid]*common.Page // dirty page cache (pages are write-once, so shared)
	freelistSavepoint freelist.TxSavepoint         // transaction-scoped freelist state
	commitHandlers    int                          // number of commit handlers registered so far
}

// Savepoint creates a named savepoint within the current writable
// transaction. If the transaction is later rolled back to this savepoint
// via RollbackToSavepoint, all changes made after this call are discarded
// while all changes made before it are retained.
//
// Savepoints are stacked: a savepoint created with an already used name
// shadows the previous one. A savepoint is not a nested transaction and
// cannot be committed independently; the changes are only persisted when
// the enclosing transaction commits.
//
// Savepoints can only be created on writable transactions.
func (tx *Tx) Savepoint(name string) error {
	if tx.db == nil {
		return berrors.ErrTxClosed
	} else if !tx.writable {
		return berrors.ErrTxNotWritable
	}

	sp := &savepoint{
		name:              name,
		pages:             maps.Clone(tx.pages),
		freelistSavepoint: tx.db.freelist.Savepoint(tx.meta.Txid()),
		commitHandlers:    len(tx.commitHandlers),
	}
	tx.meta.Copy(&sp.meta)
	sp.root = &Bucket{}
	cloneBucketInto(sp.root, &tx.root, tx)

	tx.savepoints = append(tx.savepoints, sp)
	return nil
}

// RollbackToSavepoint rolls back all changes made after the named
// savepoint was created, including bucket creations and deletions, key
// updates, sequence updates, page allocations and frees, and OnCommit
// handlers registered after the savepoint.
//
// The named savepoint stays active and can be rolled back to again, while
// all savepoints created after it are destroyed.
//
// Handle invalidation rule: all Bucket and Cursor handles obtained before
// this call are invalidated, because they may reference nodes or pages
// that have been rolled back or reallocated. Using a stale Bucket handle
// returns ErrStaleHandle from error-returning methods and panics from the
// remaining methods; using a stale Cursor panics. Fresh handles must be
// acquired from the transaction after the rollback.
func (tx *Tx) RollbackToSavepoint(name string) error {
	if tx.db == nil {
		return berrors.ErrTxClosed
	} else if !tx.writable {
		return berrors.ErrTxNotWritable
	}

	idx := tx.findSavepoint(name)
	if idx < 0 {
		return berrors.ErrSavepointNotFound
	}
	sp := tx.savepoints[idx]

	// Invalidate all Bucket and Cursor handles obtained so far; handles
	// acquired after this call carry the new generation.
	tx.generation++

	// Restore the transaction state captured by the savepoint.
	sp.meta.Copy(tx.meta)
	cloneBucketInto(&tx.root, sp.root, tx)
	tx.pages = maps.Clone(sp.pages)
	tx.db.freelist.RollbackToSavepoint(tx.meta.Txid(), sp.freelistSavepoint)
	tx.commitHandlers = tx.commitHandlers[:sp.commitHandlers]

	// The named savepoint stays active; savepoints created after it are
	// destroyed.
	tx.savepoints = tx.savepoints[:idx+1]
	return nil
}

// ReleaseSavepoint destroys the named savepoint and all savepoints created
// after it. The changes made since the savepoint are retained and become
// part of the enclosing transaction; they can no longer be rolled back to
// the released savepoint.
func (tx *Tx) ReleaseSavepoint(name string) error {
	if tx.db == nil {
		return berrors.ErrTxClosed
	} else if !tx.writable {
		return berrors.ErrTxNotWritable
	}

	idx := tx.findSavepoint(name)
	if idx < 0 {
		return berrors.ErrSavepointNotFound
	}

	// Releasing a savepoint also destroys all savepoints created after it.
	tx.savepoints = tx.savepoints[:idx]
	return nil
}

// findSavepoint returns the index of the most recently created savepoint
// with the given name, or -1 if there is none.
func (tx *Tx) findSavepoint(name string) int {
	for i := len(tx.savepoints) - 1; i >= 0; i-- {
		if tx.savepoints[i].name == name {
			return i
		}
	}
	return -1
}

// dereferenceSavepoints copies all mmap references held by savepoint
// snapshots to heap memory. It must be called before the mmap is remapped,
// for the same reason Tx.root is dereferenced.
func (tx *Tx) dereferenceSavepoints() {
	for _, sp := range tx.savepoints {
		sp.root.dereference()
	}
}

// cloneBucketInto deep-copies the bucket/node tree of src into dst. The
// cloned nodes reference dst (and the cloned child buckets) as their
// owning bucket. Key and value byte slices are shared with the source
// tree because bbolt never mutates them in place; they are only ever
// replaced wholesale. Inline page references are shared for the same
// reason.
func cloneBucketInto(dst, src *Bucket, tx *Tx) {
	*dst = Bucket{
		tx:          tx,
		gen:         tx.generation,
		page:        src.page,
		FillPercent: src.FillPercent,
	}
	dst.InBucket = &common.InBucket{}
	*dst.InBucket = *src.InBucket

	cloner := nodeCloner{cloned: make(map[*node]*node)}
	if src.rootNode != nil {
		dst.rootNode = cloner.clone(src.rootNode, dst)
	}
	if src.nodes != nil {
		dst.nodes = make(map[common.Pgid]*node, len(src.nodes))
		for pgid, n := range src.nodes {
			dst.nodes[pgid] = cloner.clone(n, dst)
		}
	}
	if src.buckets != nil {
		dst.buckets = make(map[string]*Bucket, len(src.buckets))
		for name, child := range src.buckets {
			clonedChild := &Bucket{}
			cloneBucketInto(clonedChild, child, tx)
			dst.buckets[name] = clonedChild
		}
	}
}

// nodeCloner deep-copies a node graph, preserving the sharing of nodes
// between the parent/children links and the bucket's node cache.
type nodeCloner struct {
	cloned map[*node]*node
}

func (c *nodeCloner) clone(n *node, owner *Bucket) *node {
	if n == nil {
		return nil
	}
	if cloned, ok := c.cloned[n]; ok {
		return cloned
	}
	cloned := &node{
		bucket:     owner,
		isLeaf:     n.isLeaf,
		unbalanced: n.unbalanced,
		spilled:    n.spilled,
		key:        n.key,
		pgid:       n.pgid,
		inodes:     slices.Clone(n.inodes),
	}
	c.cloned[n] = cloned
	cloned.parent = c.clone(n.parent, owner)
	if n.children != nil {
		cloned.children = make(nodes, len(n.children))
		for i, child := range n.children {
			cloned.children[i] = c.clone(child, owner)
		}
	}
	return cloned
}
