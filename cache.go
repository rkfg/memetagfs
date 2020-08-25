package main

import (
	"log"
	"strconv"
	"sync"
	"time"
)

type fileCache struct {
	data       map[string]*item
	lastUpdate time.Time
}

var (
	lastUpdate time.Time
	m          sync.Mutex
	logCache   bool
	hit        uint
	miss       uint
)

func invalidateCache() {
	m.Lock()
	defer m.Unlock()
	lastUpdate = time.Now()
	if logCache {
		log.Println("Cache invalidated")
	}
}

func newCache() *fileCache {
	return &fileCache{data: map[string]*item{}, lastUpdate: time.Now()}
}

func (f *fileCache) ensureValid() {
	if f.lastUpdate.Before(lastUpdate) {
		if logCache {
			log.Println("Cache reset")
		}
		f.data = map[string]*item{}
		f.lastUpdate = time.Now()
	}
}

func (f *fileCache) get(name string) (i *item, ok bool) {
	f.ensureValid()
	result, ok := f.data[name]
	if logCache {
		if ok {
			hit++
			log.Printf("Cache hit! %.2f%% ineff", float32(miss)*100/(float32(hit+miss)))
		} else {
			miss++
			log.Printf("Cache miss... %.2f%% ineff", float32(miss)*100/(float32(hit+miss)))
		}
	}
	return result, ok
}

func (f *fileCache) getByID(id id) (i *item, ok bool) {
	return f.get(strconv.FormatUint(uint64(id), 10))
}

func (f *fileCache) put(name string, i *item) {
	f.ensureValid()
	f.data[name] = i
}

func (f *fileCache) putID(id id, i *item) {
	f.put(strconv.FormatUint(uint64(id), 10), i)
}

func (f *fileCache) putMissing(name string) {
	f.put(name, &item{missing: true})
}

func (f *fileCache) putMissingID(id id) {
	f.putID(id, &item{missing: true})
}
