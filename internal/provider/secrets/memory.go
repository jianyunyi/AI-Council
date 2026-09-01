package secrets

import (
	"sort"
	"sync"
)

type MemoryVault struct {
	mu     sync.RWMutex
	values map[string][]byte
}

func NewMemoryVault() *MemoryVault {
	return &MemoryVault{values: make(map[string][]byte)}
}

func (v *MemoryVault) Put(id, value string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if previous, ok := v.values[id]; ok {
		zero(previous)
	}
	v.values[id] = append([]byte(nil), value...)
}

func (v *MemoryVault) Get(id string) (string, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	value, ok := v.values[id]
	if !ok {
		return "", false
	}
	copyOfValue := append([]byte(nil), value...)
	return string(copyOfValue), true
}

func (v *MemoryVault) IDs() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	ids := make([]string, 0, len(v.values))
	for id := range v.values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (v *MemoryVault) Delete(id string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if value, ok := v.values[id]; ok {
		zero(value)
		delete(v.values, id)
	}
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
