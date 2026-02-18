package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Counter wraps prometheus.Counter
type Counter struct {
	counter prometheus.Counter
}

// NewCounter creates a new counter metric
func NewCounter(name string, labels map[string]string) *Counter {
	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Name:        name,
		Help:        name,
		ConstLabels: labels,
	})
	// Try to register, but ignore AlreadyRegisteredError
	if err := prometheus.Register(counter); err != nil {
		// If already registered, try to get the existing one
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := are.ExistingCollector.(prometheus.Counter); ok {
				return &Counter{counter: existing}
			}
		}
		// For other errors, continue with unregistered counter
	}
	return &Counter{counter: counter}
}

// Inc increments the counter by 1
func (c *Counter) Inc() {
	c.counter.Inc()
}

// Add adds the given value to the counter
func (c *Counter) Add(v float64) {
	c.counter.Add(v)
}

// Gauge wraps prometheus.Gauge
type Gauge struct {
	gauge prometheus.Gauge
}

// NewGauge creates a new gauge metric
func NewGauge(name string, labels map[string]string) *Gauge {
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        name,
		Help:        name,
		ConstLabels: labels,
	})
	// Try to register, but ignore AlreadyRegisteredError
	if err := prometheus.Register(gauge); err != nil {
		// If already registered, try to get the existing one
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := are.ExistingCollector.(prometheus.Gauge); ok {
				return &Gauge{gauge: existing}
			}
		}
		// For other errors, continue with unregistered gauge
	}
	return &Gauge{gauge: gauge}
}

// Set sets the gauge to the given value
func (g *Gauge) Set(v float64) {
	g.gauge.Set(v)
}

// Inc increments the gauge by 1
func (g *Gauge) Inc() {
	g.gauge.Inc()
}

// Dec decrements the gauge by 1
func (g *Gauge) Dec() {
	g.gauge.Dec()
}

// Add adds the given value to the gauge
func (g *Gauge) Add(v float64) {
	g.gauge.Add(v)
}

// Sub subtracts the given value from the gauge
func (g *Gauge) Sub(v float64) {
	g.gauge.Sub(v)
}

// Histogram wraps prometheus.Histogram
type Histogram struct {
	histogram prometheus.Histogram
}

// NewHistogram creates a new histogram metric
func NewHistogram(name string, labels map[string]string, buckets []float64) *Histogram {
	histogram := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:        name,
		Help:        name,
		ConstLabels: labels,
		Buckets:     buckets,
	})
	// Try to register, but ignore AlreadyRegisteredError
	if err := prometheus.Register(histogram); err != nil {
		// If already registered, try to get the existing one
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := are.ExistingCollector.(prometheus.Histogram); ok {
				return &Histogram{histogram: existing}
			}
		}
		// For other errors, continue with unregistered histogram
	}
	return &Histogram{histogram: histogram}
}

// Observe adds a single observation to the histogram
func (h *Histogram) Observe(v float64) {
	h.histogram.Observe(v)
}
