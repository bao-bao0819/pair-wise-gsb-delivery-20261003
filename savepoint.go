package bbolt

import (
	berrors "go.etcd.io/bbolt/errors"
	"go.etcd.io/bbolt/internal/common"
	"go.etcd.io/bbolt/internal/freelist"
)

// savepoint represents a named snapshot of the in-memory state of a
// writable transaction. It allows rolling back part of the transaction
// without aborting it entirely. A savepoint is not an independent
// transaction: it cannot be committed on its own, and it disappears
// when the enclosing transaction is committed or rolled back.
type savepoint struct {
	name string

	// meta is a copy of the transaction meta (including the page
	// high-water mark) taken when the savepoint was created.
	meta common.Meta

	// root is a deep clone of the transaction's root bucket, including
	// all cached sub-buckets and materialized nodes.
	root *Bucket

	// freelist is a deep copy of the database freelist.
	freelist freelist.Interface

	// pages is a copy of the transaction's dirty page cache.
	pages map[common.Pgid]*common.Page

	// commitHandlers is the number of commit handlers registered on the
	// transaction when the savepoint was created.
	commitHandlers int
}

// Savepoint creates a named savepoint inside a writable transaction.
// If a later operation fails, RollbackToSavepoint can be used to undo
// all changes made after this point without aborting the whole transaction.
//
// A savepoint is not an independent transaction: only Commit of the
// enclosing transaction persists the changes. Savepoints disappear when
// the transaction is committed or rolled back.
//
// The name must be unique among the currently active savepoints of the
// transaction; otherwise ErrSavepointExists is returned.
func (tx *Tx) Savepoint(name string) error {
	if tx.db == nil {
		return berrors.ErrTxClosed
	} else if !tx.writable {
		return berrors.ErrTxNotWritable
	}
	for _, sp := range tx.savepoints {
		if sp.name == name {
			return berrors.ErrSavepointExists
		}
	}

	sp := &savepoint{
		name:           name,
		root:           tx.root.deepClone(),
		freelist:       tx.db.freelist.Clone(),
		pages:          make(map[common.Pgid]*common.Page, len(tx.pages)),
		commitHandlers: len(tx.commitHandlers),
	}
	tx.meta.Copy(&sp.meta)
	for id, p := range tx.pages {
		sp.pages[id] = p
	}

	tx.savepoints = append(tx.savepoints, sp)
	return nil
}

// RollbackToSavepoint undoes all changes made after the named savepoint
// was created. The savepoint itself remains active and can be rolled back
// to again; savepoints created after it are discarded.
//
// Handle validity rule: all Bucket and Cursor handles obtained before this
// call are invalidated, because they may reference nodes or pages that have
// been rolled back or that can be reallocated. They must not be used anymore:
// methods returning an error return ErrSavepointRolledBack, while other
// methods panic. Handles must be re-acquired from the transaction after the
// rollback.
//
// Commit handlers registered after the savepoint are discarded as well.
func (tx *Tx) RollbackToSavepoint(name string) error {
	if tx.db == nil {
		return berrors.ErrTxClosed
	} else if !tx.writable {
		return berrors.ErrTxNotWritable
	}
	idx := -1
	for i, sp := range tx.savepoints {
		if sp.name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return berrors.ErrSavepointNotFound
	}
	sp := tx.savepoints[idx]

	// Invalidate all outstanding Bucket/Cursor handles: they may reference
	// nodes or pages that are about to be rolled back or reallocated.
	tx.epoch++

	// Restore the meta (including the page high-water mark).
	sp.meta.Copy(tx.meta)

	// Restore the bucket/node tree. A fresh clone is installed so that the
	// savepoint stays intact and can be rolled back to again.
	tx.root = *sp.root.deepClone()

	// Restore the freelist: pages freed after the savepoint become in-use
	// again and pages allocated after the savepoint become free again.
	tx.db.freelist.Reset(sp.freelist)

	// Drop dirty pages allocated after the savepoint.
	tx.pages = make(map[common.Pgid]*common.Page, len(sp.pages))
	for id, p := range sp.pages {
		tx.pages[id] = p
	}

	// Drop commit handlers registered after the savepoint.
	tx.commitHandlers = tx.commitHandlers[:sp.commitHandlers]

	// Discard savepoints created after this one, but keep this one active.
	tx.savepoints = tx.savepoints[:idx+1]
	return nil
}

// ReleaseSavepoint discards the named savepoint and all savepoints created
// after it. It does not undo any change made by the transaction.
func (tx *Tx) ReleaseSavepoint(name string) error {
	if tx.db == nil {
		return berrors.ErrTxClosed
	} else if !tx.writable {
		return berrors.ErrTxNotWritable
	}
	for i, sp := range tx.savepoints {
		if sp.name == name {
			tx.savepoints = tx.savepoints[:i]
			return nil
		}
	}
	return berrors.ErrSavepointNotFound
}

// deepClone returns a deep copy of the bucket, including all cached
// sub-buckets and materialized nodes. Key and value byte slices are shared
// with the original bucket because bbolt never mutates them in place.
// The clone is stamped with the transaction's current handle epoch.
func (b *Bucket) deepClone() *Bucket {
	c := &Bucket{
		InBucket:    &common.InBucket{},
		tx:          b.tx,
		page:        b.page,
		FillPercent: b.FillPercent,
		epoch:       b.tx.epoch,
	}
	*c.InBucket = *b.InBucket

	if b.buckets != nil {
		c.buckets = make(map[string]*Bucket, len(b.buckets))
		for name, child := range b.buckets {
			c.buckets[name] = child.deepClone()
		}
	}

	if b.nodes != nil {
		c.nodes = make(map[common.Pgid]*node, len(b.nodes))
		cloned := make(map[*node]*node, len(b.nodes))
		for pgId, n := range b.nodes {
			c.nodes[pgId] = n.deepClone(c, cloned)
		}
		if b.rootNode != nil {
			c.rootNode = b.rootNode.deepClone(c, cloned)
		}
	} else if b.rootNode != nil {
		c.rootNode = b.rootNode.deepClone(c, make(map[*node]*node))
	}

	return c
}

// deepClone returns a deep copy of the node, preserving the parent/children
// structure. The cloned map memoizes already cloned nodes so that shared
// nodes (reachable both from the bucket's node cache and from the
// parent/children links) are cloned exactly once.
func (n *node) deepClone(b *Bucket, cloned map[*node]*node) *node {
	if nn, ok := cloned[n]; ok {
		return nn
	}
	nn := &node{
		bucket:     b,
		isLeaf:     n.isLeaf,
		unbalanced: n.unbalanced,
		spilled:    n.spilled,
		key:        n.key,
		pgid:       n.pgid,
	}
	cloned[n] = nn
	if n.inodes != nil {
		nn.inodes = make(common.Inodes, len(n.inodes))
		copy(nn.inodes, n.inodes)
	}
	if n.parent != nil {
		nn.parent = n.parent.deepClone(b, cloned)
	}
	if n.children != nil {
		nn.children = make(nodes, len(n.children))
		for i, child := range n.children {
			nn.children[i] = child.deepClone(b, cloned)
		}
	}
	return nn
}
