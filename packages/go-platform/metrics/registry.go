package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

type Registry struct {
	mu       sync.RWMutex
	counters map[string]float64
}

func NewRegistry() *Registry {
	return &Registry{counters: map[string]float64{}}
}

func (r *Registry) Inc(name string, labels map[string]string) {
	r.Add(name, labels, 1)
}

func (r *Registry) Add(name string, labels map[string]string, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[counterKey(name, labels)] += value
}

func (r *Registry) WritePrometheus(w io.Writer) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.counters))
	for key := range r.counters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		_, _ = fmt.Fprintf(w, "%s %g\n", key, r.counters[key])
	}
}

func counterKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", key, labels[key]))
	}
	return fmt.Sprintf("%s{%s}", name, strings.Join(parts, ","))
}
