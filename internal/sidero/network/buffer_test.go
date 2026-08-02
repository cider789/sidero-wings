package network

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBufferCalculatesRatesAndBoundsSamples(t *testing.T) {
	b := NewBuffer(2)
	t0 := time.Unix(100, 0).UTC()
	b.Add(Counters{ReceiveBytes: 100, TransmitBytes: 50}, t0)
	sample := b.Add(Counters{ReceiveBytes: 300, TransmitBytes: 150}, t0.Add(2*time.Second))
	require.Equal(t, float64(100), sample.ReceiveRate)
	require.Equal(t, float64(50), sample.TransmitRate)
	b.Add(Counters{ReceiveBytes: 10, TransmitBytes: 5}, t0.Add(4*time.Second))
	require.Len(t, b.Recent(), 1)
	require.Equal(t, float64(0), b.Recent()[0].ReceiveRate)
}

func TestBufferMarksUnavailableAndDoesNotBridgeRatesAcrossRestart(t *testing.T) {
	b := NewBuffer(4)
	t0 := time.Unix(100, 0).UTC()
	b.Add(Counters{ReceiveBytes: 100, TransmitBytes: 50}, t0)
	unavailable := b.Unavailable(t0.Add(time.Second))
	require.False(t, unavailable.Available)
	sample := b.Add(Counters{ReceiveBytes: 200, TransmitBytes: 100}, t0.Add(2*time.Second))
	require.Zero(t, sample.ReceiveRate)
}
