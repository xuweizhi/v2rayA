package iptables

import (
	"sync"
	"time"
)

// LocalIPWatcher invokes functions when interface IPs change
type LocalIPWatcher struct {
	ticker      *time.Ticker
	stopCh      chan struct{}
	stopOnce    sync.Once
	closed      bool
	cidrPool    map[string]struct{}
	mu          sync.Mutex
	AddedFunc   func(cidr string)
	RemovedFunc func(cidr string)
	UpdateFunc  func(cidrs []string)
}

func NewLocalIPWatcher(interval time.Duration, AddedFunc func(cidr string), RemovedFunc func(cidr string)) *LocalIPWatcher {
	w := &LocalIPWatcher{
		ticker:      time.NewTicker(interval),
		stopCh:      make(chan struct{}),
		cidrPool:    make(map[string]struct{}),
		AddedFunc:   AddedFunc,
		RemovedFunc: RemovedFunc,
	}
	w.SyncIP()
	go func() {
		for {
			select {
			case <-w.stopCh:
				return
			case <-w.ticker.C:
				w.SyncIP()
			}
		}
	}()
	return w
}

func (w *LocalIPWatcher) Close() error {
	w.mu.Lock()
	w.closed = true
	w.AddedFunc = func(cidr string) {}
	w.RemovedFunc = w.AddedFunc
	w.cidrPool = nil
	w.ticker.Stop()
	w.mu.Unlock()
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
	return nil
}

func (w *LocalIPWatcher) SyncIP() {
	cidrs, err := GetLocalCIDR()
	if err != nil {
		return
	}
	m := make(map[string]struct{}, len(cidrs))

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	for _, cidr := range cidrs {
		if _, seen := m[cidr]; seen {
			continue
		}
		m[cidr] = struct{}{}
		if _, ok := w.cidrPool[cidr]; !ok {
			w.AddedFunc(cidr)
		}
	}
	for cidr := range w.cidrPool {
		if _, ok := m[cidr]; !ok {
			w.RemovedFunc(cidr)
		}
	}
	w.cidrPool = m
}
