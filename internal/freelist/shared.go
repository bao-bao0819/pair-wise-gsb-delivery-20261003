package freelist

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"unsafe"

	"go.etcd.io/bbolt/internal/common"
)

type txPending struct {
	ids              []common.Pgid
	alloctx          []common.Txid // txids allocating the ids
	lastReleaseBegin common.Txid   // beginning txid of last matching releaseRange
}

type shared struct {
	Interface

	readonlyTXIDs []common.Txid               // all readonly transaction IDs.
	allocs        map[common.Pgid]common.Txid // mapping of Txid that allocated a pgid.
	allocSpans    map[common.Pgid]uint64      // mapping of allocated span start pgid to its size in pages.
	cache         map[common.Pgid]struct{}    // fast lookup of all free and pending page ids.
	pending       map[common.Txid]*txPending  // mapping of soon-to-be free page ids by tx.
}

func newShared() *shared {
	return &shared{
		pending:    make(map[common.Txid]*txPending),
		allocs:     make(map[common.Pgid]common.Txid),
		allocSpans: make(map[common.Pgid]uint64),
		cache:      make(map[common.Pgid]struct{}),
	}
}

func (t *shared) pendingPageIds() map[common.Txid]*txPending {
	return t.pending
}

func (t *shared) PendingCount() int {
	var count int
	for _, txp := range t.pending {
		count += len(txp.ids)
	}
	return count
}

func (t *shared) Count() int {
	return t.FreeCount() + t.PendingCount()
}

func (t *shared) Freed(pgId common.Pgid) bool {
	_, ok := t.cache[pgId]
	return ok
}

func (t *shared) Free(txid common.Txid, p *common.Page) {
	if p.Id() <= 1 {
		panic(fmt.Sprintf("cannot free page 0 or 1: %d", p.Id()))
	}

	// Free page and all its overflow pages.
	txp := t.pending[txid]
	if txp == nil {
		txp = &txPending{}
		t.pending[txid] = txp
	}
	allocTxid, ok := t.allocs[p.Id()]
	common.Verify(func() {
		if allocTxid == txid {
			panic(fmt.Sprintf("free: freed page (%d) was allocated by the same transaction (%d)", p.Id(), txid))
		}
	})
	if ok {
		delete(t.allocs, p.Id())
		delete(t.allocSpans, p.Id())
	}

	for id := p.Id(); id <= p.Id()+common.Pgid(p.Overflow()); id++ {
		// Verify that page is not already free.
		if _, ok := t.cache[id]; ok {
			panic(fmt.Sprintf("page %d already freed", id))
		}
		// Add to the freelist and cache.
		txp.ids = append(txp.ids, id)
		txp.alloctx = append(txp.alloctx, allocTxid)
		t.cache[id] = struct{}{}
	}
}

func (t *shared) Rollback(txid common.Txid) {
	// Remove page ids from cache.
	txp := t.pending[txid]
	if txp == nil {
		return
	}
	for i, pgid := range txp.ids {
		delete(t.cache, pgid)
		tx := txp.alloctx[i]
		if tx == 0 {
			continue
		}
		if tx != txid {
			// Pending free aborted; restore page back to alloc list.
			t.allocs[pgid] = tx
		} else {
			// A writing TXN should never free a page which was allocated by itself.
			panic(fmt.Sprintf("rollback: freed page (%d) was allocated by the same transaction (%d)", pgid, txid))
		}
	}
	// Remove pages from pending list and mark as free if allocated by txid.
	delete(t.pending, txid)

	// Remove pgids which are allocated by this txid
	for pgid, tid := range t.allocs {
		if tid == txid {
			delete(t.allocs, pgid)
			delete(t.allocSpans, pgid)
		}
	}
}

// Savepoint implements Interface.Savepoint.
func (t *shared) Savepoint(txid common.Txid) TxSavepoint {
	sp := TxSavepoint{}
	if txp := t.pending[txid]; txp != nil {
		sp.hasPending = true
		sp.pendingIds = slices.Clone(txp.ids)
		sp.pendingAllocs = slices.Clone(txp.alloctx)
	}
	for start, atxid := range t.allocs {
		if atxid != txid {
			continue
		}
		if sp.allocSpans == nil {
			sp.allocSpans = make(map[common.Pgid]uint64)
		}
		sp.allocSpans[start] = t.allocSpans[start]
	}
	return sp
}

// RollbackToSavepoint implements Interface.RollbackToSavepoint.
func (t *shared) RollbackToSavepoint(txid common.Txid, sp TxSavepoint) {
	// Undo the pages freed by the transaction after the savepoint was taken.
	// The pending list of a writable transaction only ever grows while the
	// transaction is open, so the savepoint's list is always a prefix of
	// the current one.
	if txp := t.pending[txid]; txp != nil {
		common.Verify(func() {
			if len(txp.ids) < len(sp.pendingIds) {
				panic(fmt.Sprintf("savepoint pending list (%d) longer than current pending list (%d)", len(sp.pendingIds), len(txp.ids)))
			}
			for i, id := range sp.pendingIds {
				if txp.ids[i] != id {
					panic(fmt.Sprintf("savepoint pending list is not a prefix of the current pending list at index %d", i))
				}
			}
		})
		for i := len(sp.pendingIds); i < len(txp.ids); i++ {
			pgid := txp.ids[i]
			delete(t.cache, pgid)
			if atx := txp.alloctx[i]; atx != 0 {
				// The page was allocated by an older transaction and its
				// allocation record was removed when it was freed; restore it.
				t.allocs[pgid] = atx
			}
		}
		if sp.hasPending {
			txp.ids = slices.Clone(sp.pendingIds)
			txp.alloctx = slices.Clone(sp.pendingAllocs)
		} else {
			delete(t.pending, txid)
		}
	}

	// Undo the pages allocated by the transaction after the savepoint was
	// taken by returning them to the free list.
	var released []common.Pgid
	for start, atxid := range t.allocs {
		if atxid != txid {
			continue
		}
		if _, ok := sp.allocSpans[start]; ok {
			continue
		}
		size := t.allocSpans[start]
		delete(t.allocs, start)
		delete(t.allocSpans, start)
		for i := common.Pgid(0); i < common.Pgid(size); i++ {
			id := start + i
			t.cache[id] = struct{}{}
			released = append(released, id)
		}
	}
	t.mergeSpans(released)
}

func (t *shared) AddReadonlyTXID(tid common.Txid) {
	t.readonlyTXIDs = append(t.readonlyTXIDs, tid)
}

func (t *shared) RemoveReadonlyTXID(tid common.Txid) {
	for i := range t.readonlyTXIDs {
		if t.readonlyTXIDs[i] == tid {
			last := len(t.readonlyTXIDs) - 1
			t.readonlyTXIDs[i] = t.readonlyTXIDs[last]
			t.readonlyTXIDs = t.readonlyTXIDs[:last]
			break
		}
	}
}

type txIDx []common.Txid

func (t txIDx) Len() int           { return len(t) }
func (t txIDx) Swap(i, j int)      { t[i], t[j] = t[j], t[i] }
func (t txIDx) Less(i, j int) bool { return t[i] < t[j] }

func (t *shared) ReleasePendingPages() {
	// Free all pending pages prior to the earliest open transaction.
	sort.Sort(txIDx(t.readonlyTXIDs))
	minid := common.Txid(math.MaxUint64)
	if len(t.readonlyTXIDs) > 0 {
		minid = t.readonlyTXIDs[0]
	}
	if minid > 0 {
		t.release(minid - 1)
	}
	// Release unused txid extents.
	for _, tid := range t.readonlyTXIDs {
		t.releaseRange(minid, tid-1)
		minid = tid + 1
	}
	t.releaseRange(minid, common.Txid(math.MaxUint64))
	// Any page both allocated and freed in an extent is safe to release.
}

func (t *shared) release(txid common.Txid) {
	m := make(common.Pgids, 0)
	for tid, txp := range t.pending {
		if tid <= txid {
			// Move transaction's pending pages to the available freelist.
			// Don't remove from the cache since the page is still free.
			m = append(m, txp.ids...)
			delete(t.pending, tid)
		}
	}
	t.mergeSpans(m)
}

func (t *shared) releaseRange(begin, end common.Txid) {
	if begin > end {
		return
	}
	m := common.Pgids{}
	for tid, txp := range t.pending {
		if tid < begin || tid > end {
			continue
		}
		// Don't recompute freed pages if ranges haven't updated.
		if txp.lastReleaseBegin == begin {
			continue
		}
		for i := 0; i < len(txp.ids); i++ {
			if atx := txp.alloctx[i]; atx < begin || atx > end {
				continue
			}
			m = append(m, txp.ids[i])
			txp.ids[i] = txp.ids[len(txp.ids)-1]
			txp.ids = txp.ids[:len(txp.ids)-1]
			txp.alloctx[i] = txp.alloctx[len(txp.alloctx)-1]
			txp.alloctx = txp.alloctx[:len(txp.alloctx)-1]
			i--
		}
		txp.lastReleaseBegin = begin
		if len(txp.ids) == 0 {
			delete(t.pending, tid)
		}
	}
	t.mergeSpans(m)
}

// Copyall copies a list of all free ids and all pending ids in one sorted list.
// f.count returns the minimum length required for dst.
func (t *shared) Copyall(dst []common.Pgid) {
	m := make(common.Pgids, 0, t.PendingCount())
	for _, txp := range t.pendingPageIds() {
		m = append(m, txp.ids...)
	}
	sort.Sort(m)
	common.Mergepgids(dst, t.freePageIds(), m)
}

func (t *shared) Reload(p *common.Page) {
	t.Read(p)
	t.NoSyncReload(t.freePageIds())
}

func (t *shared) NoSyncReload(pgIds common.Pgids) {
	// Build a cache of only pending pages.
	pcache := make(map[common.Pgid]struct{})
	for _, txp := range t.pending {
		for _, pendingID := range txp.ids {
			pcache[pendingID] = struct{}{}
		}
	}

	// Check each page in the freelist and build a new available freelist
	// with any pages not in the pending lists.
	a := []common.Pgid{}
	for _, id := range pgIds {
		if _, ok := pcache[id]; !ok {
			a = append(a, id)
		}
	}

	t.Init(a)
}

// reindex rebuilds the free cache based on available and pending free lists.
func (t *shared) reindex() {
	free := t.freePageIds()
	pending := t.pendingPageIds()
	t.cache = make(map[common.Pgid]struct{}, len(free))
	for _, id := range free {
		t.cache[id] = struct{}{}
	}
	for _, txp := range pending {
		for _, pendingID := range txp.ids {
			t.cache[pendingID] = struct{}{}
		}
	}
}

func (t *shared) Read(p *common.Page) {
	if !p.IsFreelistPage() {
		panic(fmt.Sprintf("invalid freelist page: %d, page type is %s", p.Id(), p.Typ()))
	}

	ids := p.FreelistPageIds()

	// Copy the list of page ids from the freelist.
	if len(ids) == 0 {
		t.Init([]common.Pgid{})
	} else {
		// copy the ids, so we don't modify on the freelist page directly
		idsCopy := make([]common.Pgid, len(ids))
		copy(idsCopy, ids)
		// Make sure they're sorted.
		sort.Sort(common.Pgids(idsCopy))

		t.Init(idsCopy)
	}
}

func (t *shared) EstimatedWritePageSize() int {
	n := t.Count()
	if n >= 0xFFFF {
		// The first element will be used to store the count. See freelist.write.
		n++
	}
	return int(common.PageHeaderSize) + (int(unsafe.Sizeof(common.Pgid(0))) * n)
}

func (t *shared) Write(p *common.Page) {
	// Combine the old free pgids and pgids waiting on an open transaction.

	// Update the header flag.
	p.SetFlags(common.FreelistPageFlag)

	// The page.count can only hold up to 64k elements so if we overflow that
	// number then we handle it by putting the size in the first element.
	l := t.Count()
	if l == 0 {
		p.SetCount(uint16(l))
	} else if l < 0xFFFF {
		p.SetCount(uint16(l))
		data := common.UnsafeAdd(unsafe.Pointer(p), unsafe.Sizeof(*p))
		ids := unsafe.Slice((*common.Pgid)(data), l)
		t.Copyall(ids)
	} else {
		p.SetCount(0xFFFF)
		data := common.UnsafeAdd(unsafe.Pointer(p), unsafe.Sizeof(*p))
		ids := unsafe.Slice((*common.Pgid)(data), l+1)
		ids[0] = common.Pgid(l)
		t.Copyall(ids[1:])
	}
}
