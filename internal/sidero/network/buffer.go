// Package network normalizes cumulative container counters into bounded samples.
package network

import (
	"sync"
	"time"
)

type Counters struct {
	ReceiveBytes    uint64
	TransmitBytes   uint64
	ReceivePackets  uint64
	TransmitPackets uint64
	ReceiveDrops    uint64
	TransmitDrops   uint64
}
type Sample struct {
	ReceiveBytes    uint64    `json:"receive_bytes"`
	TransmitBytes   uint64    `json:"transmit_bytes"`
	ReceiveRate     float64   `json:"receive_rate"`
	TransmitRate    float64   `json:"transmit_rate"`
	ReceivePackets  uint64    `json:"receive_packets,omitempty"`
	TransmitPackets uint64    `json:"transmit_packets,omitempty"`
	ReceiveDrops    uint64    `json:"receive_drops,omitempty"`
	TransmitDrops   uint64    `json:"transmit_drops,omitempty"`
	Timestamp       time.Time `json:"sampled_at"`
	Available       bool      `json:"available"`
}
type Buffer struct {
	mu      sync.RWMutex
	maximum int
	samples []Sample
}

func NewBuffer(maximum int) *Buffer {
	if maximum < 1 {
		maximum = 1
	}
	return &Buffer{maximum: maximum}
}
func (b *Buffer) Add(c Counters, now time.Time) Sample {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := Sample{ReceiveBytes: c.ReceiveBytes, TransmitBytes: c.TransmitBytes, ReceivePackets: c.ReceivePackets, TransmitPackets: c.TransmitPackets, ReceiveDrops: c.ReceiveDrops, TransmitDrops: c.TransmitDrops, Timestamp: now.UTC(), Available: true}
	if len(b.samples) > 0 {
		previous := b.samples[len(b.samples)-1]
		if c.ReceiveBytes < previous.ReceiveBytes || c.TransmitBytes < previous.TransmitBytes {
			b.samples = nil
			b.samples = append(b.samples, s)
			return s
		}
		seconds := s.Timestamp.Sub(previous.Timestamp).Seconds()
		if previous.Available && seconds > 0 && c.ReceiveBytes >= previous.ReceiveBytes && c.TransmitBytes >= previous.TransmitBytes {
			s.ReceiveRate = float64(c.ReceiveBytes-previous.ReceiveBytes) / seconds
			s.TransmitRate = float64(c.TransmitBytes-previous.TransmitBytes) / seconds
		}
	}
	b.samples = append(b.samples, s)
	if len(b.samples) > b.maximum {
		b.samples = append([]Sample(nil), b.samples[len(b.samples)-b.maximum:]...)
	}
	return s
}
func (b *Buffer) Unavailable(now time.Time) Sample {
	b.mu.Lock()
	defer b.mu.Unlock()
	sample := Sample{Timestamp: now.UTC(), Available: false}
	b.samples = append(b.samples, sample)
	if len(b.samples) > b.maximum {
		b.samples = append([]Sample(nil), b.samples[len(b.samples)-b.maximum:]...)
	}
	return sample
}
func (b *Buffer) Latest() (Sample, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.samples) == 0 {
		return Sample{Timestamp: time.Now().UTC()}, false
	}
	latest := b.samples[len(b.samples)-1]
	return latest, latest.Available
}
func (b *Buffer) Recent() []Sample {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]Sample(nil), b.samples...)
}
func (b *Buffer) Reset() { b.mu.Lock(); b.samples = nil; b.mu.Unlock() }
