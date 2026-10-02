golang-lru
==========

This provides the `lru` package which implements a fixed-size
thread safe LRU cache. It is based on the cache in Groupcache.

Documentation
=============

Full docs are available on [Go Packages](https://pkg.go.dev/github.com/hashicorp/golang-lru/v2)

LRU cache example
=================

```go
package main

import (
	"fmt"
	"github.com/hashicorp/golang-lru/v2"
)

func main() {
	l, _ := lru.New[int, any](128)
	for i := 0; i < 256; i++ {
		l.Add(i, nil)
	}
	if l.Len() != 128 {
		panic(fmt.Sprintf("bad len: %v", l.Len()))
	}
}
```

Expirable LRU cache example
===========================

```go
package main

import (
	"fmt"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
)

func main() {
	// make cache with 10ms TTL and 5 max keys
	cache := expirable.NewLRU[string, string](5, nil, time.Millisecond*10)


	// set value under key1.
	cache.Add("key1", "val1")

	// get value under key1
	r, ok := cache.Get("key1")

	// check for OK value
	if ok {
		fmt.Printf("value before expiration is found: %v, value: %q\n", ok, r)
	}

	// wait for cache to expire
	time.Sleep(time.Millisecond * 12)

	// get value under key1 after key expiration
	r, ok = cache.Get("key1")
	fmt.Printf("value after expiration is found: %v, value: %q\n", ok, r)

	// set value under key2, would evict old entry because it is already expired.
	cache.Add("key2", "val2")

	fmt.Printf("Cache len: %d\n", cache.Len())
	// Output:
	// value before expiration is found: true, value: "val1"
	// value after expiration is found: false, value: ""
	// Cache len: 1
}
```

Ordered snapshots and restore
=============================

Rebuilding a cache (for example after persistence or a restart) often needs
to preserve recency order. Reading each value back with `Get` would rewrite
that order as a side effect. `Snapshot` and `Restore` provide an ordered view
and a matching restore without touching recency:

```go
// Snapshot entries are ordered oldest to newest and do not change recent-ness.
entries := l.Snapshot()

// entries is plain caller-owned data; serialize it in any format you like.

// Restore validates the slice first (duplicate keys are rejected with an error
// wrapping lru.ErrDuplicateKey and the old cache is left untouched) and then
// replaces the cache contents atomically. If there are more entries than the
// capacity, the oldest surplus entries are dropped.
if err := l.Restore(entries); err != nil {
	log.Fatal(err)
}
```

The snapshot data is held by the caller: the library never binds it to a file
format. Values are shallow-copied, so for pointer, slice, or map values the
snapshot and the cache alias the same underlying value; deep copy yourself if
you need isolation. Replacing a populated cache fires the eviction callback
for every removed entry, as with `Purge`; entries dropped for exceeding the
capacity were never stored and do not fire it.
