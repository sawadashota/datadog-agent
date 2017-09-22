// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2017 Datadog, Inc.

// NOTE: This file contains a feature in development that is NOT supported.

package aggregator

import (
	"github.com/DataDog/datadog-agent/pkg/metrics"
	"github.com/DataDog/datadog-agent/pkg/metrics/percentile"
)

// Distribution tracks the distribution of samples added over one flush
// period. Designed to be globally accurate for percentiles.
type Distribution struct {
	sketch percentile.QSketch
	count  int64
}

// NewDistribution creates a new Distribution
func NewDistribution() *Distribution {
	return &Distribution{sketch: percentile.NewQSketch()}
}

func (d *Distribution) addSample(sample *metrics.MetricSample, timestamp float64) {
	// Insert sample value into the sketch
	d.sketch = d.sketch.Add(sample.Value)
	d.count++
}

func (d *Distribution) flush(timestamp float64) (*percentile.SketchSeries, error) {
	if d.count == 0 {
		return &percentile.SketchSeries{}, percentile.NoSketchError{}
	}
	sketch := &percentile.SketchSeries{
		Sketches: []percentile.Sketch{{Timestamp: int64(timestamp),
			Sketch: d.sketch}},
	}
	// reset the global histogram
	d.sketch = percentile.NewQSketch()
	d.count = 0

	return sketch, nil
}
