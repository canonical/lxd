package metrics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMetricSet_FilterSamples(t *testing.T) {
	labels := map[string]string{"foo": "1", "bar": "2", "baz": "3"}

	m := NewMetricSet(labels)
	require.Equal(t, labels, m.labels)
	m.AddSamples(CPUSecondsTotal, Sample{Value: 10})
	require.Equal(t, []Sample{{Value: 10, Labels: labels}}, m.set[CPUSecondsTotal])

	// newSet should contain the sample
	newSet := m.FilterSamples(map[string]string{"bar": "2"})
	require.Equal(t, labels, newSet.labels)
	require.Equal(t, []Sample{{Value: 10, Labels: labels}}, newSet.set[CPUSecondsTotal])

	// newSet should contain the sample
	newSet = m.FilterSamples(map[string]string{"foo": "1", "bar": "2"})
	require.Equal(t, labels, newSet.labels)
	require.Equal(t, []Sample{{Value: 10, Labels: labels}}, newSet.set[CPUSecondsTotal])

	// newSet should not contain the sample
	newSet = m.FilterSamples(map[string]string{"bar": "1"})
	require.Equal(t, labels, newSet.labels)
	require.Equal(t, []Sample(nil), newSet.set[CPUSecondsTotal])

	// newSet should not contain the sample
	newSet = m.FilterSamples(map[string]string{"bar": "1", "foo": "1"})
	require.Equal(t, labels, newSet.labels)
	require.Equal(t, []Sample(nil), newSet.set[CPUSecondsTotal])

	newSet = m.FilterSamples(nil)
	require.Equal(t, []Sample(nil), newSet.set[CPUSecondsTotal])
}
