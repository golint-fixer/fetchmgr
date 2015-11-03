package fetchmgr

import (
	"container/heap"
	"sync"
	"time"
)

// CachedFetcher caches fetched contents. It use Fetcher internally to fetch
// resources. It will call Fetcher's Fetch method thread-safely.
type CachedFetcher struct {
	fetcher  Fetcher
	ttl      time.Duration
	mutex    sync.Mutex
	cache    map[interface{}]entry
	queMutex sync.Mutex
	queue    deleteQueue
}

type entry struct {
	value func() (interface{}, error)
}

// NewCachedFetcher creates CachedFetcher
func NewCachedFetcher(
	fetcher Fetcher,
	ttl time.Duration,
) *CachedFetcher {
	cached := &CachedFetcher{
		fetcher: fetcher,
		ttl:     ttl,
		cache:   make(map[interface{}]entry),
	}

	go cached.deleteLoop()

	return cached
}

// Fetch memoizes fetcher.Fetch method.
// It calls fetcher.Fetch method and caches the return value unless there is no
// cached results. Chached values are expired when c.ttl has passed.
// If the internal Fetcher.Fetch returns err (!= nil), CachedFetcher doesn't
// cache any results.
func (c *CachedFetcher) Fetch(key interface{}) (interface{}, error) {
	e := pickEntry(c, key)
	return e.value()
}

func pickEntry(c *CachedFetcher, key interface{}) entry {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	cached, ok := c.cache[key]
	if ok {
		return cached
	}

	var val interface{}
	var err error
	done := make(chan struct{})
	go func() {
		val, err = c.fetcher.Fetch(key)
		close(done)

		if err != nil {
			// Don't reuse error values
			c.queueKey(key, 0)
			return
		}

		c.queueKey(key, c.ttl)
	}()

	lazy := func() (interface{}, error) {
		<-done
		return val, err
	}

	cached = entry{value: lazy}
	c.cache[key] = cached

	return cached
}

func (c *CachedFetcher) deleteKey(key interface{}) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	delete(c.cache, key)
}

func (c *CachedFetcher) queueKey(key interface{}, ttl time.Duration) {
	c.queMutex.Lock()
	defer c.queMutex.Unlock()

	item := deleteItem{key, time.Now().Add(ttl)}
	heap.Push(&c.queue, item)
}

func (c *CachedFetcher) deleteLoop() {
	for {
		c.queMutex.Lock()
		if c.queue.Len() > 0 {
			item := heap.Pop(&c.queue).(deleteItem)
			if item.expire.Before(time.Now()) {
				c.deleteKey(item.key)
			} else {
				heap.Push(&c.queue, item)
			}
		}
		c.queMutex.Unlock()

		time.Sleep(1 * time.Millisecond)
	}
}

type deleteItem struct {
	key    interface{}
	expire time.Time
}

type deleteQueue []deleteItem

func (dq deleteQueue) Len() int { return len(dq) }

func (dq deleteQueue) Less(i, j int) bool {
	return dq[i].expire.Before(dq[j].expire)
}

func (dq deleteQueue) Swap(i, j int) {
	dq[i], dq[j] = dq[j], dq[i]
}

func (dq *deleteQueue) Push(x interface{}) {
	*dq = append(*dq, x.(deleteItem))
}

func (dq *deleteQueue) Pop() interface{} {
	n := len(*dq)
	ret := (*dq)[n-1]
	*dq = (*dq)[0 : n-1]
	return ret
}
