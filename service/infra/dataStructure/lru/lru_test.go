package lru

import "testing"

func TestReplacingKeyDoesNotLeakOrEvictNewValue(t *testing.T) {
	cache := New(FixedLength, 2)
	for i := 0; i < 100; i++ {
		cache.Insert("same", i)
	}
	if cache.list.Len() != 1 || len(cache.reverseIndex) != 1 {
		t.Fatal("duplicate keys leaked list nodes")
	}
	cache.Insert("second", 2)
	cache.Insert("same", 100)
	cache.Insert("third", 3)
	if cache.Get("same") != 100 || cache.Get("second") != nil || cache.list.Len() != 2 {
		t.Fatal("incorrect LRU replacement/eviction")
	}
}
