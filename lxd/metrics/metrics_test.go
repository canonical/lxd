package metrics

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/entity"
	"github.com/canonical/lxd/shared/api"
)

func TestMetricSet_FilterSamples(t *testing.T) {
	labels := map[string]string{"project": "default", "name": "jammy"}
	newMetricSet := func() *MetricSet {
		m := NewMetricSet(labels)
		require.Equal(t, labels, m.labels)
		m.AddSamples(CPUSecondsTotal, Sample{Value: 10})
		require.Equal(t, []Sample{{Value: 10, Labels: labels}}, m.set[CPUSecondsTotal])
		return m
	}

	m := newMetricSet()
	permissionChecker := func(u *api.URL) bool {
		return u.String() == entity.InstanceURL("default", "jammy").String()
	}

	m.FilterSamples(permissionChecker)

	// Should still contain the sample
	require.Equal(t, []Sample{{Value: 10, Labels: labels}}, m.set[CPUSecondsTotal])

	m = newMetricSet()
	permissionChecker = func(u *api.URL) bool {
		return u.String() == entity.InstanceURL("not-default", "jammy").String()
	}

	m.FilterSamples(permissionChecker)

	// Should no longer contain the sample.
	require.Equal(t, []Sample{}, m.set[CPUSecondsTotal])

	m = NewMetricSet(map[string]string{"project": "default"})
	m.AddSamples(CPUSecondsTotal, Sample{Value: 10})

	n := NewMetricSet(map[string]string{"name": "jammy"})
	n.AddSamples(CPUSecondsTotal, Sample{Value: 20})

	m.Merge(n)

	for _, sample := range m.set[CPUSecondsTotal] {
		hasKeys := []string{}

		for k := range sample.Labels {
			hasKeys = append(hasKeys, k)
		}

		require.Contains(t, hasKeys, "project")
	}
}
