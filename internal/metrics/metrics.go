// Package metrics provides a minimal Prometheus-compatible metrics registry
// using only the standard library, so no new dependencies are introduced.
package metrics

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
)

// Counter is a monotonically increasing counter.
type Counter struct {
	val atomic.Int64
}

func (c *Counter) Inc(n int64) {
	c.val.Add(n)
}

func (c *Counter) Value() int64 {
	return c.val.Load()
}

// Gauge is a metric that can go up and down.
type Gauge struct {
	val atomic.Int64
}

func (g *Gauge) Set(n int64) {
	g.val.Store(n)
}

func (g *Gauge) Inc() {
	g.val.Add(1)
}

func (g *Gauge) Dec() {
	g.val.Add(-1)
}

func (g *Gauge) Value() int64 {
	return g.val.Load()
}

// Histogram is a simple latency histogram with fixed buckets.
type Histogram struct {
	buckets []float64
	counts  []atomic.Int64
	sum     atomic.Int64
	count   atomic.Int64
}

func NewHistogram(buckets []float64) *Histogram {
	return &Histogram{
		buckets: buckets,
		counts:  make([]atomic.Int64, len(buckets)),
	}
}

func (h *Histogram) Observe(v float64) {
	h.sum.Add(int64(v * 1e6)) // store microseconds as int64
	h.count.Add(1)
	for i, b := range h.buckets {
		if v <= b {
			h.counts[i].Add(1)
			return
		}
	}
	// overflow bucket
	h.counts[len(h.buckets)-1].Add(1)
}

// Registry holds all metrics.
type Registry struct {
	mu sync.RWMutex

	HTTPRequestsTotal   *Counter
	HTTPRequestDuration *Histogram
	PinsTotal           *Counter
	PinsByStatus        map[string]*Counter
	ObjectsTotal        *Counter
	BucketsTotal        *Gauge
	DBConnectionsOpen   *Gauge
}

func NewRegistry() *Registry {
	return &Registry{
		HTTPRequestsTotal:   &Counter{},
		HTTPRequestDuration: NewHistogram([]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}),
		PinsTotal:           &Counter{},
		PinsByStatus:        map[string]*Counter{},
		ObjectsTotal:        &Counter{},
		BucketsTotal:        &Gauge{},
		DBConnectionsOpen:   &Gauge{},
	}
}

func (r *Registry) PinStatusCounter(status string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.PinsByStatus[status]
	if !ok {
		c = &Counter{}
		r.PinsByStatus[status] = c
	}
	return c
}

// Handler returns an http.Handler that exposes metrics in Prometheus text format.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Write([]byte("# HELP meiling_http_requests_total Total HTTP requests\n"))
		w.Write([]byte("# TYPE meiling_http_requests_total counter\n"))
		w.Write([]byte(fmtCounter("meiling_http_requests_total", r.HTTPRequestsTotal.Value())))
		w.Write([]byte("# HELP meiling_http_request_duration_seconds HTTP request latency histogram\n"))
		w.Write([]byte("# TYPE meiling_http_request_duration_seconds histogram\n"))
		w.Write([]byte(fmtHistogram("meiling_http_request_duration_seconds", r.HTTPRequestDuration)))
		w.Write([]byte("# HELP meiling_pins_total Total pin requests\n"))
		w.Write([]byte("# TYPE meiling_pins_total counter\n"))
		w.Write([]byte(fmtCounter("meiling_pins_total", r.PinsTotal.Value())))
		for status, c := range r.PinsByStatus {
			w.Write([]byte(fmt.Sprintf("# HELP meiling_pin_status_total Pin requests by status (%s)\n", status)))
			w.Write([]byte(fmt.Sprintf("# TYPE meiling_pin_status_total counter\n")))
			w.Write([]byte(fmtCounter("meiling_pin_status_total{status=\""+status+"\"}", c.Value())))
		}
		w.Write([]byte("# HELP meiling_objects_total Total objects stored\n"))
		w.Write([]byte("# TYPE meiling_objects_total counter\n"))
		w.Write([]byte(fmtCounter("meiling_objects_total", r.ObjectsTotal.Value())))
		w.Write([]byte("# HELP meiling_buckets_total Total buckets\n"))
		w.Write([]byte("# TYPE meiling_buckets_total gauge\n"))
		w.Write([]byte(fmtGauge("meiling_buckets_total", r.BucketsTotal.Value())))
		w.Write([]byte("# HELP meiling_db_connections_open Open database connections\n"))
		w.Write([]byte("# TYPE meiling_db_connections_open gauge\n"))
		w.Write([]byte(fmtGauge("meiling_db_connections_open", r.DBConnectionsOpen.Value())))
	})
}

func fmtCounter(name string, v int64) []byte {
	return []byte(fmt.Sprintf("%s %d\n", name, v))
}

func fmtGauge(name string, v int64) []byte {
	return []byte(fmt.Sprintf("%s %d\n", name, v))
}

func fmtHistogram(name string, h *Histogram) []byte {
	var b []byte
	b = append(b, []byte(fmt.Sprintf("# HELP %s_bucket Histogram buckets\n", name))...)
	b = append(b, []byte(fmt.Sprintf("# TYPE %s_bucket histogram\n", name))...)
	var cum int64
	for i, bucket := range h.buckets {
		cum += h.counts[i].Load()
		b = append(b, []byte(fmt.Sprintf("%s_bucket{le=\"%g\"} %d\n", name, bucket, cum))...)
	}
	cum += h.counts[len(h.counts)-1].Load()
	b = append(b, []byte(fmt.Sprintf("%s_bucket{le=\"+Inf\"} %d\n", name, cum))...)
	b = append(b, []byte(fmt.Sprintf("%s_sum %d\n", name, h.sum.Load()))...)
	b = append(b, []byte(fmt.Sprintf("%s_count %d\n", name, h.count.Load()))...)
	return b
}
