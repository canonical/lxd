package filters

import (
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/placement/internal/models"
)

// groupCandidatesByDomain buckets candidates by their assigned failure domain, excluding any
// candidate with no domain assigned. domains lists each domain that appears among candidates, in
// first-seen order.
func groupCandidatesByDomain(candidates []db.NodeInfo, memberDomains models.MemberFailureDomains) (candidatesByDomain map[string][]db.NodeInfo, domains []string) {
	candidatesByDomain = make(map[string][]db.NodeInfo, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		domain, ok := memberDomains.Of(c.ID)
		if !ok {
			continue
		}

		candidatesByDomain[domain.Name] = append(candidatesByDomain[domain.Name], c)

		if _, ok := seen[domain.Name]; !ok {
			seen[domain.Name] = struct{}{}
			domains = append(domains, domain.Name)
		}
	}

	return candidatesByDomain, domains
}
