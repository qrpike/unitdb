/*
 * Copyright 2020 Saffat Technologies, Ltd.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package metrics

import (
	"sync"
	"time"
)

// TimeSeries capture the duration and rate of events.
type TimeSeries interface {
	Cumulative() time.Duration // Cumulative time of all sampled events.
	HMean() time.Duration      // Event duration harmonic mean.
	Avg() time.Duration        // Event duration average.
	P50() time.Duration        // Event duration nth percentiles.
	P75() time.Duration
	P95() time.Duration
	P99() time.Duration
	P999() time.Duration
	Long5p() time.Duration  // Average of the longest 5% event durations.
	Short5p() time.Duration // Average of the shortest 5% event durations.
	Max() time.Duration     // Highest event duration.
	Min() time.Duration     // Lowest event duration.
	StdDev() time.Duration  // Standard deviation.
	Range() time.Duration   // Event duration range (Max-Min).
	Time(func())
	AddTime(time.Duration)
	SetWallTime(time.Duration)
	Snapshot() TimeSeries
}

// GetOrRegisterTimeSeries returns an existing timeseries or constructs and registers a
// new StandardTimeSeries.
// Be sure to unregister the meter from the registry once it is of no use to
// allow for garbage collection.
func GetOrRegisterTimeSeries(name string, r Metrics) TimeSeries {
	return r.GetOrRegister(name, NewTimeSeries).(TimeSeries)
}

// NewTimeSeries constructs a new StandardTimeSeries using an exponentially-decaying
// sample with the same reservoir size and alpha as UNIX load averages.
// Be sure to call Stop() once the timer is of no use to allow for garbage collection.
func NewTimeSeries() TimeSeries {
	return &_TimeSeries{
		histogram: NewHistogram(NewSample(&Config{Size: 50})),
	}
}

// _TimeSeries is the standard implementation of a timeseries and uses a Histogram
// and Meter.
type _TimeSeries struct {
	histogram Histogram
	mutex     sync.Mutex
}

// Cumulative returns cumulative time of all sampled events.
func (t *_TimeSeries) Cumulative() time.Duration {
	return t.histogram.Cumulative()
}

// HMean returns event duration harmonic mean.
func (t *_TimeSeries) HMean() time.Duration {
	return t.histogram.HMean()
}

// Avg returns the average of number of events recorded.
func (t *_TimeSeries) Avg() time.Duration {
	return t.histogram.Avg()
}

// P50 returns event duration nth percentiles.
func (t *_TimeSeries) P50() time.Duration {
	return t.histogram.P50()
}

// P75 returns event duration nth percentiles.
func (t *_TimeSeries) P75() time.Duration {
	return t.histogram.P75()
}

// P95 returns event duration nth percentiles.
func (t *_TimeSeries) P95() time.Duration {
	return t.histogram.P95()
}

// P99 returns event duration nth percentiles.
func (t *_TimeSeries) P99() time.Duration {
	return t.histogram.P99()
}

// P999 returns event duration nth percentiles.
func (t *_TimeSeries) P999() time.Duration {
	return t.histogram.P999()
}

// StdDev returns standard deviation.
func (t *_TimeSeries) StdDev() time.Duration {
	return t.histogram.StdDev()
}

// Long5p returns average of the longest 5% event durations.
func (t *_TimeSeries) Long5p() time.Duration {
	return t.histogram.Long5p()
}

// Short5p returns average of the shortest 5% event durations.
func (t *_TimeSeries) Short5p() time.Duration {
	return t.histogram.Short5p()
}

// Min returns lowest event duration.
func (t *_TimeSeries) Min() time.Duration {
	return t.histogram.Min()
}

// Max returns highest event duration.
func (t *_TimeSeries) Max() time.Duration {
	return t.histogram.Max()
}

// Range returns event duration range (Max-Min).
func (t *_TimeSeries) Range() time.Duration {
	return t.histogram.Range()
}

// Record the duration of the execution of the given function.
func (t *_TimeSeries) Time(f func()) {
	ts := time.Now()
	f()
	t.AddTime(time.Since(ts))
}

// AddTime adds a time.Duration to metrics
func (t *_TimeSeries) AddTime(time time.Duration) {
	t.histogram.AddTime(time)
}

// SetWallTime optionally sets an elapsed wall time duration.
// This affects rate output by using total events counted over time.
// This is useful for concurrent/parallelized events that overlap
// in wall time and are writing to a shared metrics instance.
func (t *_TimeSeries) SetWallTime(time time.Duration) {
	t.histogram.SetWallTime(time)
}

// Snapshot returns a read-only copy of the timer.
func (t *_TimeSeries) Snapshot() TimeSeries {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return &TimeSeriesSnapshot{
		histogram: t.histogram.Snapshot().(*HistogramSnapshot),
	}
}

// TimeSeriesSnapshot is a read-only copy of another Timer.
type TimeSeriesSnapshot struct {
	histogram *HistogramSnapshot
}

// Cumulative returns cumulative time of all sampled events.
func (t *TimeSeriesSnapshot) Cumulative() time.Duration {
	return t.histogram.Cumulative()
}

// HMean returns event duration harmonic mean.
func (t *TimeSeriesSnapshot) HMean() time.Duration {
	return t.histogram.HMean()
}

// Avg returns average of number of events recorded.
func (t *TimeSeriesSnapshot) Avg() time.Duration {
	return t.histogram.Avg()
}

// P50 returns event duration nth percentiles.
func (t *TimeSeriesSnapshot) P50() time.Duration {
	return t.histogram.P50()
}

// P75 returns event duration nth percentiles.
func (t *TimeSeriesSnapshot) P75() time.Duration {
	return t.histogram.P75()
}

// P95 returns event duration nth percentiles.
func (t *TimeSeriesSnapshot) P95() time.Duration {
	return t.histogram.P95()
}

// P99 returns event duration nth percentiles.
func (t *TimeSeriesSnapshot) P99() time.Duration {
	return t.histogram.P99()
}

// P999 returns event duration nth percentiles.
func (t *TimeSeriesSnapshot) P999() time.Duration {
	return t.histogram.P999()
}

// StdDev returns standard deviation.
func (t *TimeSeriesSnapshot) StdDev() time.Duration {
	return t.histogram.StdDev()
}

// Long5p returns average of the longest 5% event durations.
func (t *TimeSeriesSnapshot) Long5p() time.Duration {
	return t.histogram.Long5p()
}

// Short5p returns average of the shortest 5% event durations.
func (t *TimeSeriesSnapshot) Short5p() time.Duration {
	return t.histogram.Short5p()
}

// Min returns lowest event duration.
func (t *TimeSeriesSnapshot) Min() time.Duration {
	return t.histogram.Min()
}

// Max returns highest event duration.
func (t *TimeSeriesSnapshot) Max() time.Duration {
	return t.histogram.Max()
}

// Range returns event duration range (Max-Min).
func (t *TimeSeriesSnapshot) Range() time.Duration {
	return t.histogram.Range()
}

// Time panics.
func (*TimeSeriesSnapshot) Time(func()) {
	panic("Time called on a TimeSeriesSnapshot")
}

// AddTime panics.
func (*TimeSeriesSnapshot) AddTime(time.Duration) {
	panic("AddTime called on a TimeSeriesSnapshot")
}

// SetWallTime panics.
func (*TimeSeriesSnapshot) SetWallTime(time.Duration) {
	panic("SetWallTime called on a TimeSeriesSnapshot")
}

// Snapshot returns the snapshot.
func (t *TimeSeriesSnapshot) Snapshot() TimeSeries { return t }
