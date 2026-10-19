package bgp

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	bgpAPI "github.com/osrg/gobgp/v4/api"
	bgpAPIUtil "github.com/osrg/gobgp/v4/pkg/apiutil"
	bgpPacket "github.com/osrg/gobgp/v4/pkg/packet/bgp"
	bgpServer "github.com/osrg/gobgp/v4/pkg/server"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

// Names of the LXD-managed GoBGP entities used to scope route exports per peer.
// These are used both when creating the entities and when cleaning up the ones
// left behind by a previous policy rebuild.
const (
	neighborSetNamePrefix = "ns_peer_"
	prefixSetNamePrefix   = "ps_peer_"
	policyNamePrefix      = "pol_lxd_export_"
	statementAcceptPrefix = "stmt_accept_"
	statementRejectPrefix = "stmt_reject_"
)

// Server represents a BGP server instance.
type Server struct {
	bgp *bgpServer.BgpServer

	// Internal state (to handle reconfiguration)
	address  string
	asn      uint32
	routerID net.IP
	paths    map[uuid.UUID]path
	peers    map[string]peer
	// policyName is the name of the currently assigned LXD export policy so it
	// can be cleaned up when the policy is next regenerated.
	policyName string

	mu sync.Mutex
	// policyMu serializes GoBGP export policy rebuilds and the BGP listener
	// lifecycle, so a concurrent configuration change cannot interleave with a
	// policy rebuild or leave a rebuild operating on a stopped/replaced server.
	policyMu sync.Mutex
}

type path struct {
	owner   string
	prefix  net.IPNet
	nexthop net.IP
}

type peer struct {
	address  net.IP
	asn      uint32
	password string
	holdtime uint64
	owners   map[string]int
}

// ownsPath reports whether the routes owned by pathOwner belong to this peer,
// i.e. whether they should be advertised to it. A peer may be shared by several
// networks, so this is true when any of the peer's owners owns the path, either
// directly or through a sub-owner (a forward, load balancer or instance NIC).
func (p peer) ownsPath(pathOwner string) bool {
	for owner := range p.owners {
		if ownerMatches(owner, pathOwner) {
			return true
		}
	}

	return false
}

// usedByOwner reports whether this peer is used by owner or by one of its
// sub-owners, so that a change to owner's routes is known to affect this peer.
func (p peer) usedByOwner(owner string) bool {
	for peerOwner := range p.owners {
		if ownerMatches(owner, peerOwner) || ownerMatches(peerOwner, owner) {
			return true
		}
	}

	return false
}

// ownerNames returns the identifiers of the networks sharing this peer, sorted
// so that debug output is stable.
func (p peer) ownerNames() []string {
	owners := make([]string, 0, len(p.owners))
	for owner := range p.owners {
		owners = append(owners, owner)
	}

	slices.Sort(owners)

	return owners
}

// refCount returns the total number of references to this peer across all of its
// owners.
func (p peer) refCount() int {
	count := 0
	for _, refs := range p.owners {
		count += refs
	}

	return count
}

// NewServer returns a new server instance.
func NewServer() *Server {
	// Setup new struct.
	s := &Server{
		paths: map[uuid.UUID]path{},
		peers: map[string]peer{},
	}

	return s
}

// Start sets up the BGP listener.
func (s *Server) start(address string, asn uint32, routerID net.IP) error {
	// If routerID is nil, fill with our best guess.
	if routerID == nil || routerID.To4() == nil {
		return ErrBadRouterID
	}

	// Check if already running
	if s.bgp != nil {
		return errors.New("BGP listener is already running")
	}

	// Get the address and port before any state is changed, so that an address
	// that cannot be used fails without starting anything.
	addrHost, addrPort, err := net.SplitHostPort(address)
	if err != nil {
		addrHost = address
		addrPort = "179"
	}

	if addrHost == "" {
		addrHost = "::"
	}

	addrPortInt, err := strconv.ParseInt(addrPort, 10, 32)
	if err != nil {
		return fmt.Errorf("Invalid BGP listen port in address %q: %w", address, err)
	}

	// Setup the listener configuration.
	conf := &bgpAPI.Global{
		RouterId: routerID.String(),
		Asn:      asn,

		// Always setup for IPv4 and IPv6.
		Families: []uint32{0, 1},

		// Listen address.
		ListenAddresses: []string{addrHost},
		ListenPort:      int32(addrPortInt),
	}

	// Spawn the BGP goroutines, keeping the new server local until it is
	// serving. A server recorded before it is serving would be reported as
	// already running by the check above and could never be started again.
	bgpSrv := bgpServer.NewBgpServer()
	go bgpSrv.Serve()

	// Start the listener.
	err = bgpSrv.StartBgp(context.Background(), &bgpAPI.StartBgpRequest{Global: conf})
	if err != nil {
		bgpSrv.Stop()
		return err
	}

	s.bgp = bgpSrv

	// The listener is up, so a failure from here has to take it down again
	// rather than leave it recorded, which would leave BGP unusable until the
	// daemon restarts as nothing could configure it.
	reverter := revert.New()
	defer reverter.Fail()

	// Copy the peer and path lists, so they can be restored if re-registering
	// them fails part way through.
	oldPeers := map[string]peer{}
	maps.Copy(oldPeers, s.peers)

	oldPaths := map[uuid.UUID]path{}
	maps.Copy(oldPaths, s.paths)

	reverter.Add(func() {
		// Stop the listener directly rather than through stop(), which also
		// deletes the recorded peers and fails if one of them was never
		// registered with this listener.
		bgpSrv.Stop()
		s.bgp = nil
		s.peers = oldPeers
		s.paths = oldPaths
		s.policyName = ""
	})

	// Add existing paths.
	s.paths = map[uuid.UUID]path{}
	for _, path := range oldPaths {
		err := s.addPrefix(path.prefix, path.nexthop, path.owner)
		if err != nil {
			logger.Warn("Cannot add prefix to BGP server", logger.Ctx{"prefix": path.prefix.String(), "err": err})
		}
	}

	// Re-register the existing peers with the new listener, restoring each peer
	// with the owners it had so that their reference counts survive the restart.
	s.peers = map[string]peer{}
	for _, oldPeer := range oldPeers {
		err := s.addPeerToServer(oldPeer.address, oldPeer.asn, oldPeer.password, oldPeer.holdtime)
		if err != nil {
			return err
		}

		s.peers[oldPeer.address.String()] = oldPeer
	}

	// Record the address.
	s.address = address
	s.asn = asn
	s.routerID = routerID

	reverter.Success()
	return nil
}

// Stop tears down the BGP listener.
//
// The teardown runs to completion whatever fails along the way, and the first
// failure is returned. Leaving it half done would be worse than reporting the
// failure: a listener that is only partly torn down is still recorded as running
// and refuses to start again, so it could neither be used nor reconfigured.
func (s *Server) stop() error {
	// Skip if no instance.
	if s.bgp == nil {
		return nil
	}

	// Save the peer list.
	oldPeers := map[string]peer{}
	maps.Copy(oldPeers, s.peers)

	var firstErr error

	// Remove all the peers from the BGP server.
	for _, peer := range oldPeers {
		err := s.removePeerFromServer(peer.address)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// Restore peer list, so that a later start can re-register them.
	s.peers = oldPeers

	// Stop the listener.
	err := s.bgp.StopBgp(context.Background(), &bgpAPI.StopBgpRequest{})
	if err != nil && firstErr == nil {
		firstErr = err
	}

	// Mark the daemon as down.
	s.address = ""
	s.asn = 0
	s.routerID = nil
	s.bgp = nil
	s.policyName = ""

	return firstErr
}

// Configure updates the listener with a new configuration..
func (s *Server) Configure(address string, asn uint32, routerID net.IP) error {
	// Serialize with policy rebuilds and other lifecycle changes so that the
	// GoBGP server cannot be stopped/replaced between a policy snapshot and the
	// policy API calls issued by updatePoliciesLocked.
	s.policyMu.Lock()
	defer s.policyMu.Unlock()

	s.mu.Lock()
	err := s.configure(address, asn, routerID)
	s.mu.Unlock()
	if err != nil {
		return err
	}

	return s.updatePoliciesLocked()
}

func (s *Server) configure(address string, asn uint32, routerID net.IP) error {
	// Store current configuration for reverting.
	oldAddress := s.address
	oldASN := s.asn
	oldRouterID := s.routerID

	// Setup reverter.
	revert := revert.New()
	defer revert.Fail()

	// Stop the listener.
	err := s.stop()
	if err != nil {
		return fmt.Errorf("Failed stopping current listener: %w", err)
	}

	// Check if we should start.
	if address != "" && asn > 0 && routerID != nil {
		// Restore old address on failure.
		revert.Add(func() { _ = s.start(oldAddress, oldASN, oldRouterID) })

		// Start the listener with the new address.
		err = s.start(address, asn, routerID)
		if err != nil {
			return fmt.Errorf("Failed starting new listener: %w", err)
		}
	}

	// All done.
	revert.Success()
	return nil
}

// AddPrefix adds a new prefix to the BGP server.
func (s *Server) AddPrefix(subnet net.IPNet, nexthop net.IP, owner string) error {
	// Locking.
	s.mu.Lock()
	err := s.addPrefix(subnet, nexthop, owner)
	s.mu.Unlock()
	if err != nil {
		return err
	}

	return s.updatePolicies(owner)
}

func (s *Server) addPrefix(subnet net.IPNet, nexthop net.IP, owner string) error {
	// Reject a prefix that would be advertised to one of the owner's peers with
	// a second next-hop.
	err := s.checkPrefixNexthop(subnet, nexthop, owner)
	if err != nil {
		return err
	}

	// Prepare the prefix.
	prefix, err := netip.ParsePrefix(subnet.String())
	if err != nil {
		return err
	}

	nlri, err := bgpPacket.NewIPAddrPrefix(prefix)
	if err != nil {
		return err
	}

	attrs := []bgpPacket.PathAttributeInterface{bgpPacket.NewPathAttributeOrigin(0)}

	// Add the prefix to the server.
	var pathUUID uuid.UUID
	if s.bgp != nil {
		nextHop, err := netip.ParseAddr(nexthop.String())
		if err != nil {
			return err
		}

		family := bgpPacket.RF_IPv4_UC
		if subnet.IP.To4() != nil {
			// IPv4 prefix.
			aNextHop, err := bgpPacket.NewPathAttributeNextHop(nextHop)
			if err != nil {
				return err
			}

			attrs = append(attrs, aNextHop)
		} else {
			// IPv6 prefix.
			family = bgpPacket.RF_IPv6_UC
			v6Attrs, err := bgpPacket.NewPathAttributeMpReachNLRI(family, []bgpPacket.PathNLRI{{NLRI: nlri}}, nextHop)
			if err != nil {
				return err
			}

			attrs = append(attrs, v6Attrs)
		}

		responses, err := s.bgp.AddPath(bgpAPIUtil.AddPathRequest{
			Paths: []*bgpAPIUtil.Path{{
				Family: family,
				Nlri:   nlri,
				Attrs:  attrs,
			}},
		})
		if err != nil {
			return err
		}

		if len(responses) != 1 {
			return fmt.Errorf("Expected one response when adding BGP path, got %d", len(responses))
		}

		if responses[0].Error != nil {
			return responses[0].Error
		}

		pathUUID = responses[0].UUID
	} else {
		// Generate a dummy UUID.
		pathUUID = uuid.New()
	}

	// Add path to the map.
	s.paths[pathUUID] = path{
		prefix:  subnet,
		nexthop: nexthop,
		owner:   owner,
	}

	return nil
}

// checkPrefixNexthop returns an error if the given route would be advertised to
// one of the owner's peers through a second next-hop.
func (s *Server) checkPrefixNexthop(subnet net.IPNet, nexthop net.IP, owner string) error {
	// The route is not recorded until the check passes, so it is considered here
	// alongside the paths already in place.
	candidate := path{owner: owner, prefix: subnet, nexthop: nexthop}

	for _, p := range s.peers {
		if !p.ownsPath(owner) {
			continue
		}

		err := s.checkPeerPrefixNexthops(p, &candidate)
		if err != nil {
			return err
		}
	}

	return nil
}

// checkPeerPrefixNexthops returns an error if the routes belonging to peer p
// would advertise one prefix to it through more than one next-hop. The optional
// candidate path is considered alongside the recorded paths, so that a route can
// be validated before it is added. Passing nil checks the recorded state, which
// is what a peer joining a shared session needs.
//
// A session advertises a prefix once, through one next-hop: the export policy is
// grouped by next-hop, and GoBGP holds a single path per prefix unless ADD-PATH
// is negotiated, so a second next-hop for the same prefix would replace the first
// rather than be advertised alongside it. Only networks sharing one peer can
// produce that, because a prefix's next-hop is a property of the network
// advertising it, so the configuration is reported rather than one of the routes
// being dropped silently.
func (s *Server) checkPeerPrefixNexthops(p peer, candidate *path) error {
	nexthopsByPrefix := map[string][]string{}

	addPath := func(existing path) {
		if !p.ownsPath(existing.owner) {
			return
		}

		prefixStr := existing.prefix.String()
		nexthopsByPrefix[prefixStr] = append(nexthopsByPrefix[prefixStr], existing.nexthop.String())
	}

	for _, existing := range s.paths {
		addPath(existing)
	}

	if candidate != nil {
		addPath(*candidate)
	}

	// Report the same conflict every time: the prefixes in sorted order, and the
	// lowest pair of next-hops advertised for the one that conflicts.
	prefixes := make([]string, 0, len(nexthopsByPrefix))
	for prefixStr := range nexthopsByPrefix {
		prefixes = append(prefixes, prefixStr)
	}

	slices.Sort(prefixes)

	for _, prefixStr := range prefixes {
		nexthops := nexthopsByPrefix[prefixStr]
		slices.Sort(nexthops)

		for i := 1; i < len(nexthops); i++ {
			if nexthops[i] != nexthops[i-1] {
				return fmt.Errorf("Prefix %q would be advertised to peer %q with more than one next-hop (%q and %q)", prefixStr, p.address.String(), nexthops[i-1], nexthops[i])
			}
		}
	}

	return nil
}

// RemovePrefixByOwner removes all prefixes for the provided owner.
//
// The owner must match exactly, so removing a network's own prefixes leaves the
// routes of the instance NICs attached to it in place. Those are owned by the
// network and individually removed by the device that added them, not with the
// network; removing them here would drop them with nothing to re-add them.
func (s *Server) RemovePrefixByOwner(owner string) error {
	// Locking.
	s.mu.Lock()

	// Make a copy of the paths dict to safely iterate (path removal mutates it).
	paths := map[uuid.UUID]path{}
	maps.Copy(paths, s.paths)

	// Iterate through the paths and remove them from the server.
	for pathUUID, path := range paths {
		if path.owner == owner {
			err := s.removePrefixByUUID(pathUUID)
			if err != nil {
				s.mu.Unlock()
				return err
			}
		}
	}

	s.mu.Unlock()

	return s.updatePolicies(owner)
}

// RemovePrefix removes a prefix from the BGP server.
func (s *Server) RemovePrefix(subnet net.IPNet, nexthop net.IP) error {
	// Locking.
	s.mu.Lock()
	err := s.removePrefix(subnet, nexthop)
	s.mu.Unlock()
	if err != nil {
		return err
	}

	return s.updatePolicies()
}

func (s *Server) removePrefix(subnet net.IPNet, nexthop net.IP) error {
	found := false
	for pathUUID, path := range s.paths {
		if !path.prefix.IP.Equal(subnet.IP) || !bytes.Equal(path.prefix.Mask, subnet.Mask) || !path.nexthop.Equal(nexthop) {
			continue
		}

		found = true

		// Remove the prefix.
		err := s.removePrefixByUUID(pathUUID)
		if err != nil {
			return err
		}
	}

	if !found {
		return ErrPrefixNotFound
	}

	return nil
}

func (s *Server) removePrefixByUUID(pathUUID uuid.UUID) error {
	// Remove it from the BGP server.
	if s.bgp != nil {
		err := s.bgp.DeletePath(bgpAPIUtil.DeletePathRequest{UUIDs: []uuid.UUID{pathUUID}})
		if err != nil && !strings.HasSuffix(err.Error(), "find a specified path(s) with the given UUID(s)") {
			return err
		}
	}

	// Remove the path from the map.
	delete(s.paths, pathUUID)

	return nil
}

// AddPeer adds a new BGP peer.
func (s *Server) AddPeer(address net.IP, asn uint32, password string, holdTime uint64, owner string) error {
	// Locking.
	s.mu.Lock()
	err := s.addPeer(address, asn, password, holdTime, owner)
	s.mu.Unlock()
	if err != nil {
		return err
	}

	return s.updatePolicies()
}

func (s *Server) addPeer(address net.IP, asn uint32, password string, holdTime uint64, owner string) error {
	addrStr := address.String()

	// Build the peer as it would be with this owner recorded, without changing
	// the stored peer or registering a session until the whole addition is known
	// to be valid.
	bgpPeer, bgpPeerExists := s.peers[addrStr]
	if bgpPeerExists {
		if bgpPeer.asn != asn {
			return fmt.Errorf("Peer %q already used but with differing ASN (%d vs %d)", addrStr, asn, bgpPeer.asn)
		}

		if subtle.ConstantTimeCompare([]byte(bgpPeer.password), []byte(password)) != 1 {
			return fmt.Errorf("Peer %q already used but with a different password", addrStr)
		}

		if bgpPeer.holdtime != holdTime {
			return fmt.Errorf("Peer %q already used but with differing hold time (%d vs %d)", addrStr, holdTime, bgpPeer.holdtime)
		}

		// Copy the owners before recording this one, so that a rejected addition
		// leaves the stored counts untouched.
		owners := make(map[string]int, len(bgpPeer.owners)+1)
		maps.Copy(owners, bgpPeer.owners)
		owners[owner]++

		bgpPeer.owners = owners
	} else {
		bgpPeer = peer{
			address:  address,
			asn:      asn,
			password: password,
			holdtime: holdTime,
			owners:   map[string]int{owner: 1},
		}
	}

	// Reject a peer that would advertise one prefix through more than one
	// next-hop. This is what a network starting on a peer another network already
	// advertises the same prefix to over, as two uplinks sharing one upstream
	// router do.
	err := s.checkPeerPrefixNexthops(bgpPeer, nil)
	if err != nil {
		return err
	}

	if !bgpPeerExists {
		err = s.addPeerToServer(address, asn, password, holdTime)
		if err != nil {
			return err
		}
	}

	s.peers[addrStr] = bgpPeer

	return nil
}

// addPeerToServer registers a peer with the underlying BGP server, without
// touching the in-memory peer list. A peer is registered once no matter how many
// networks share it, as GoBGP keys sessions by neighbour address.
func (s *Server) addPeerToServer(address net.IP, asn uint32, password string, holdTime uint64) error {
	addrStr := address.String()

	// Setup the configuration.
	n := &bgpAPI.Peer{
		// Peer information.
		Conf: &bgpAPI.PeerConf{
			NeighborAddress: addrStr,
			PeerAsn:         uint32(asn),
			AuthPassword:    password,
		},

		// Allow for 120s offline before route removal.
		GracefulRestart: &bgpAPI.GracefulRestart{
			Enabled:     true,
			RestartTime: 3600,
		},

		// Always allow for the maximum multihop.
		EbgpMultihop: &bgpAPI.EbgpMultihop{
			Enabled:     true,
			MultihopTtl: 255,
		},
	}

	// Add hold time if configured.
	if holdTime > 0 {
		n.Timers = &bgpAPI.Timers{
			Config: &bgpAPI.TimersConfig{
				HoldTime: holdTime,
			},
		}
	}

	// Setup peer for dual-stack.
	n.AfiSafis = make([]*bgpAPI.AfiSafi, 0)
	for _, routeFamily := range []bgpPacket.Family{bgpPacket.RF_IPv4_UC, bgpPacket.RF_IPv6_UC} {
		apiFamily := &bgpAPI.Family{
			Afi:  bgpAPI.Family_Afi(routeFamily.Afi()),
			Safi: bgpAPI.Family_Safi(routeFamily.Safi()),
		}

		n.AfiSafis = append(n.AfiSafis, &bgpAPI.AfiSafi{
			MpGracefulRestart: &bgpAPI.MpGracefulRestart{
				Config: &bgpAPI.MpGracefulRestartConfig{
					Enabled: true,
				},
			},
			Config: &bgpAPI.AfiSafiConfig{Family: apiFamily},
		})
	}

	// Add the peer.
	if s.bgp != nil {
		err := s.bgp.AddPeer(context.Background(), &bgpAPI.AddPeerRequest{Peer: n})
		if err != nil {
			return err
		}
	}

	return nil
}

// removePeerFromServer deletes a peer from the underlying BGP server, without
// touching the in-memory peer list.
func (s *Server) removePeerFromServer(address net.IP) error {
	if s.bgp == nil {
		return nil
	}

	return s.bgp.DeletePeer(context.Background(), &bgpAPI.DeletePeerRequest{Address: address.String()})
}

// RemovePeer removes an owner's use of a BGP peer. When other owners still share
// the session the peer is kept, and its export policy is rebuilt so that the
// departing owner's routes are withdrawn from it.
func (s *Server) RemovePeer(address net.IP, owner string) error {
	// Locking.
	s.mu.Lock()
	err := s.removePeer(address, owner)

	// Capture the owners the peer still has, if it outlived this one, before
	// releasing the lock.
	remainingOwners := s.peers[address.String()].ownerNames()
	s.mu.Unlock()

	if err != nil {
		return err
	}

	if len(remainingOwners) > 0 {
		// The peer is still in use by the other owners that share it. The
		// departing owner's routes have to be withdrawn from it, but scoping the
		// soft reset by that owner no longer matches the peer, so scope it by the
		// owners that remain.
		return s.updatePolicies(remainingOwners...)
	}

	return s.updatePolicies(owner)
}

func (s *Server) removePeer(address net.IP, owner string) error {
	addrStr := address.String()

	// Find the peer.
	bgpPeer, bgpPeerExists := s.peers[addrStr]
	if !bgpPeerExists {
		return ErrPeerNotFound
	}

	// Release this owner's reference. The session is shared with any other
	// owners, so it is only torn down once the last of them releases it.
	//
	// An owner that holds no reference has nothing to release. That happens
	// when a network configures a peer the server refused to add, because its
	// ASN, password or hold time does not match the peer another network has
	// already configured. Releasing it must stay a no-op rather than an error
	// so that stopping or updating that network still succeeds.
	refs, ownerExists := bgpPeer.owners[owner]
	if !ownerExists {
		return nil
	}

	if refs == 1 {
		delete(bgpPeer.owners, owner)
	} else {
		bgpPeer.owners[owner] = refs - 1
	}

	if len(bgpPeer.owners) > 0 {
		s.peers[addrStr] = bgpPeer
		return nil
	}

	err := s.removePeerFromServer(address)
	if err != nil {
		return err
	}

	delete(s.peers, addrStr)

	return nil
}

// policySnapshot returns the state needed to rebuild the export policy, taken
// under the server lock. The snapshot is iterated after the lock is released, so
// it must not share anything mutable with the live state: each peer's owners are
// cloned, as RemovePeer deletes from them while holding the lock.
func (s *Server) policySnapshot() (*bgpServer.BgpServer, string, map[string]peer, map[uuid.UUID]path) {
	s.mu.Lock()
	defer s.mu.Unlock()

	peers := make(map[string]peer, len(s.peers))
	for address, bgpPeer := range s.peers {
		bgpPeer.owners = maps.Clone(bgpPeer.owners)
		peers[address] = bgpPeer
	}

	paths := make(map[uuid.UUID]path, len(s.paths))
	maps.Copy(paths, s.paths)

	return s.bgp, s.policyName, peers, paths
}

// setPolicyName records the name of the currently applied LXD export policy so
// that it can be cleaned up on the next rebuild.
func (s *Server) setPolicyName(name string) {
	s.mu.Lock()
	s.policyName = name
	s.mu.Unlock()
}

// peerTag returns a GoBGP-name-safe tag for a peer address (e.g. "10.0.0.1" ->
// "10_0_0_1").
func peerTag(addr string) string {
	return strings.ReplaceAll(strings.ReplaceAll(addr, ":", "_"), ".", "_")
}

// isManagedSet reports whether a defined set name was created by LXD.
func isManagedSet(name string) bool {
	return strings.HasPrefix(name, neighborSetNamePrefix) || strings.HasPrefix(name, prefixSetNamePrefix)
}

// listManagedSets returns the LXD-managed defined sets currently present in GoBGP.
// GoBGP requires DefinedType to be specified when listing defined sets.
func listManagedSets(ctx context.Context, bgpServer *bgpServer.BgpServer) ([]*bgpAPI.DefinedSet, error) {
	sets := make([]*bgpAPI.DefinedSet, 0)
	for _, dt := range []bgpAPI.DefinedType{
		bgpAPI.DefinedType_DEFINED_TYPE_PREFIX,
		bgpAPI.DefinedType_DEFINED_TYPE_NEIGHBOR,
	} {
		err := bgpServer.ListDefinedSet(ctx, &bgpAPI.ListDefinedSetRequest{DefinedType: dt}, func(ds *bgpAPI.DefinedSet) {
			if isManagedSet(ds.Name) {
				sets = append(sets, ds)
			}
		})
		if err != nil {
			return nil, fmt.Errorf("Failed listing defined sets of type %v: %w", dt, err)
		}
	}

	return sets, nil
}

// deleteDefinedSets best-effort deletes the given defined sets, logging and
// ignoring errors as the sets may already be gone or still be referenced.
func deleteDefinedSets(ctx context.Context, bgpServer *bgpServer.BgpServer, sets []*bgpAPI.DefinedSet) {
	for _, ds := range sets {
		err := bgpServer.DeleteDefinedSet(ctx, &bgpAPI.DeleteDefinedSetRequest{
			All:        true,
			DefinedSet: ds,
		})
		if err != nil {
			logger.Warn("Failed deleting defined set", logger.Ctx{"set": ds.Name, "err": err})
		}
	}
}

// deleteExportPolicy removes a previously applied LXD export policy and any defined
// sets that are no longer referenced. The policy is deleted first (with All:true so
// its statements are removed too), which releases the sets it referenced and allows
// them to be deleted. Errors are tolerated as the policy or sets may already be gone
// (e.g. after a listener restart or a partially failed previous rebuild).
func deleteExportPolicy(ctx context.Context, bgpServer *bgpServer.BgpServer, policyName string, sets []*bgpAPI.DefinedSet) {
	if policyName != "" {
		err := bgpServer.DeletePolicy(ctx, &bgpAPI.DeletePolicyRequest{
			All:    true,
			Policy: &bgpAPI.Policy{Name: policyName},
		})
		if err != nil {
			logger.Warn("Failed deleting previous BGP export policy", logger.Ctx{"policy": policyName, "err": err})
		}
	}

	deleteDefinedSets(ctx, bgpServer, sets)
}

// buildExportPolicy builds the export policy that scopes each peer's route
// advertisements to the routes owned by that peer's network (and its sub-owners),
// rewriting the next-hop to the route's specific next-hop address. Routes are
// grouped by next-hop, so each next-hop advertised to a peer becomes one set of
// prefixes and one accept statement. It returns nil if no statements are required.
//
// The defined sets created while building the policy are also returned so that the
// caller can clean them up if a later stage of the rebuild fails. On error, any
// sets created before the failure are returned alongside the error for cleanup.
func buildExportPolicy(ctx context.Context, bgpServer *bgpServer.BgpServer, peers map[string]peer, paths map[uuid.UUID]path) (*bgpAPI.Policy, []*bgpAPI.DefinedSet, error) {
	// Group paths by owner.
	pathsByOwner := map[string][]path{}
	for _, p := range paths {
		pathsByOwner[p.owner] = append(pathsByOwner[p.owner], p)
	}

	// Generate a unique generation suffix so that neighbour set names cannot
	// collide with sets left behind by a partially failed previous rebuild.
	generation := uuid.New().String()

	statements := make([]*bgpAPI.Statement, 0)
	createdSets := make([]*bgpAPI.DefinedSet, 0)
	for _, peer := range peers {
		peerStatements, peerSets, err := buildPeerStatements(ctx, bgpServer, peer, pathsByOwner, generation)
		createdSets = append(createdSets, peerSets...)
		if err != nil {
			return nil, createdSets, err
		}

		statements = append(statements, peerStatements...)
	}

	if len(statements) == 0 {
		return nil, createdSets, nil
	}

	return &bgpAPI.Policy{
		Name:       policyNamePrefix + generation,
		Statements: statements,
	}, createdSets, nil
}

// sortedNexthops returns the distinct next-hops of the given paths, sorted so
// that the statements generated for them are built in a stable order.
//
// Each next-hop becomes one accept statement listing the prefixes advertised
// through it. A prefix is advertised to a peer with one next-hop, which AddPrefix
// and addPeer both enforce, so it matches exactly one statement (see
// Server.checkPrefixNexthop and Server.checkPeerPrefixNexthops). Should a prefix
// ever reach two statements anyway, the first one wins, and sorting keeps which
// one that is the same across policy rebuilds.
func sortedNexthops(pathsByNexthop map[string][]path) []string {
	nexthops := make([]string, 0, len(pathsByNexthop))
	for nhStr := range pathsByNexthop {
		nexthops = append(nexthops, nhStr)
	}

	slices.Sort(nexthops)

	return nexthops
}

// buildPeerStatements creates the GoBGP defined sets and statements for a single
// peer: an accept statement per next-hop of the peer's owned routes (rewriting
// the next-hop), followed by a catch-all reject statement. The defined sets created
// are returned so the caller can clean them up if a later stage of the rebuild fails.
//
// The peer's routes are grouped by next-hop: each distinct next-hop becomes one
// PrefixSet listing every prefix advertised through that address, plus one accept
// statement rewriting the next-hop to it. A prefix therefore belongs to a single
// set, as a session advertises a prefix with one next-hop only (see
// Server.checkPrefixNexthop).
//
// A peer with no matching owned routes gets no statements or sets at all: it is
// already covered by the global default reject, so creating a redundant NeighborSet
// and reject statement would only add policy churn.
func buildPeerStatements(ctx context.Context, bgpServer *bgpServer.BgpServer, peer peer, pathsByOwner map[string][]path, generation string) ([]*bgpAPI.Statement, []*bgpAPI.DefinedSet, error) {
	// Find all paths belonging to any of this peer's owners (either the owner
	// itself, or related owners like forwards, load balancers or instance NICs).
	pathsByNexthop := map[string][]path{}
	for owner, ownerPaths := range pathsByOwner {
		if !peer.ownsPath(owner) {
			continue
		}

		for _, p := range ownerPaths {
			nhStr := p.nexthop.String()
			pathsByNexthop[nhStr] = append(pathsByNexthop[nhStr], p)
		}
	}

	// Nothing to advertise to this peer; rely on the global default reject.
	if len(pathsByNexthop) == 0 {
		return nil, nil, nil
	}

	addrStr := peer.address.String()
	tag := peerTag(addrStr)
	nsName := neighborSetNamePrefix + tag + "_" + generation
	createdSets := make([]*bgpAPI.DefinedSet, 0, 1)

	// Create NeighborSet for this peer (using /32 for IPv4, /128 for IPv6).
	peerCIDR := addrStr + "/32"
	if peer.address.To4() == nil {
		peerCIDR = addrStr + "/128"
	}

	ns := &bgpAPI.DefinedSet{
		DefinedType: bgpAPI.DefinedType_DEFINED_TYPE_NEIGHBOR,
		Name:        nsName,
		List:        []string{peerCIDR},
	}

	err := bgpServer.AddDefinedSet(ctx, &bgpAPI.AddDefinedSetRequest{DefinedSet: ns})
	if err != nil {
		return nil, createdSets, fmt.Errorf("Failed adding neighbor defined set for peer %q: %w", addrStr, err)
	}

	createdSets = append(createdSets, ns)

	statements := make([]*bgpAPI.Statement, 0, len(pathsByNexthop)+1)

	// For each next-hop, create a prefix set and accept statement rewriting the
	// next-hop to that address.
	for _, nhStr := range sortedNexthops(pathsByNexthop) {
		nexthopPaths := pathsByNexthop[nhStr]
		stmt, ps, err := buildAcceptStatement(ctx, bgpServer, peerTag(addrStr), nsName, nhStr, nexthopPaths)
		if ps != nil {
			createdSets = append(createdSets, ps)
		}

		if err != nil {
			return nil, createdSets, err
		}

		statements = append(statements, stmt)
	}

	// Reject any other route advertisements toward this peer.
	statements = append(statements, &bgpAPI.Statement{
		Name: statementRejectPrefix + tag + "_" + uuid.New().String(),
		Conditions: &bgpAPI.Conditions{
			NeighborSet: &bgpAPI.MatchSet{
				Type: bgpAPI.MatchSet_TYPE_ANY,
				Name: nsName,
			},
		},
		Actions: &bgpAPI.Actions{
			RouteAction: bgpAPI.RouteAction_ROUTE_ACTION_REJECT,
		},
	})

	return statements, createdSets, nil
}

// buildAcceptStatement creates a prefix set and a statement that accepts the given
// prefixes from the peer's neighbour set and rewrites the next-hop to nhStr. The
// created prefix set is returned so the caller can clean it up if a later stage of
// the rebuild fails.
func buildAcceptStatement(ctx context.Context, bgpServer *bgpServer.BgpServer, tag string, nsName string, nhStr string, paths []path) (*bgpAPI.Statement, *bgpAPI.DefinedSet, error) {
	stmtTag := uuid.New().String()
	psName := prefixSetNamePrefix + tag + "_" + stmtTag

	prefixes := make([]*bgpAPI.Prefix, 0, len(paths))
	for _, p := range paths {
		ones, _ := p.prefix.Mask.Size()
		prefixes = append(prefixes, &bgpAPI.Prefix{
			IpPrefix:      p.prefix.String(),
			MaskLengthMin: uint32(ones),
			MaskLengthMax: uint32(ones),
		})
	}

	ps := &bgpAPI.DefinedSet{
		DefinedType: bgpAPI.DefinedType_DEFINED_TYPE_PREFIX,
		Name:        psName,
		Prefixes:    prefixes,
	}

	err := bgpServer.AddDefinedSet(ctx, &bgpAPI.AddDefinedSetRequest{DefinedSet: ps})
	if err != nil {
		return nil, nil, fmt.Errorf("Failed adding prefix defined set %q: %w", psName, err)
	}

	stmt := &bgpAPI.Statement{
		Name: statementAcceptPrefix + tag + "_" + stmtTag,
		Conditions: &bgpAPI.Conditions{
			NeighborSet: &bgpAPI.MatchSet{
				Type: bgpAPI.MatchSet_TYPE_ANY,
				Name: nsName,
			},
			PrefixSet: &bgpAPI.MatchSet{
				Type: bgpAPI.MatchSet_TYPE_ANY,
				Name: psName,
			},
		},
		Actions: &bgpAPI.Actions{
			Nexthop: &bgpAPI.NexthopAction{
				Address: nhStr,
			},
			RouteAction: bgpAPI.RouteAction_ROUTE_ACTION_ACCEPT,
		},
	}

	return stmt, ps, nil
}

// applyExportPolicy atomically sets the global export assignment to the given
// policy (with an explicit reject default so only matching routes are exported).
// A nil policy applies a reject-all assignment, used when there is nothing to
// advertise.
func applyExportPolicy(ctx context.Context, bgpServer *bgpServer.BgpServer, policy *bgpAPI.Policy) error {
	assignment := &bgpAPI.PolicyAssignment{
		Direction:     bgpAPI.PolicyDirection_POLICY_DIRECTION_EXPORT,
		DefaultAction: bgpAPI.RouteAction_ROUTE_ACTION_REJECT,
	}

	if policy != nil {
		assignment.Policies = []*bgpAPI.Policy{policy}
	}

	err := bgpServer.SetPolicyAssignment(ctx, &bgpAPI.SetPolicyAssignmentRequest{Assignment: assignment})
	if err != nil {
		return fmt.Errorf("Failed assigning BGP export policy: %w", err)
	}

	return nil
}

// softResetPeers soft-resets outbound advertisements on peers whose owner matches
// one of affectedOwners. When affectedOwners is empty, all peers are reset.
func softResetPeers(ctx context.Context, bgpServer *bgpServer.BgpServer, peers map[string]peer, affectedOwners []string) {
	for _, peer := range peers {
		if len(affectedOwners) > 0 {
			affected := false
			for _, owner := range affectedOwners {
				if peer.usedByOwner(owner) {
					affected = true
					break
				}
			}

			if !affected {
				continue
			}
		}

		_ = bgpServer.ResetPeer(ctx, &bgpAPI.ResetPeerRequest{
			Address:   peer.address.String(),
			Soft:      true,
			Direction: bgpAPI.ResetPeerRequest_DIRECTION_OUT,
		})
	}
}

// updatePolicies regenerates and applies the global export routing policy in GoBGP.
// For every peer, it creates policy statements matching the peer's NeighborSet and the
// PrefixSets of routes belonging to that peer's owner network/device. Each route statement
// sets the NexthopAction to the route's specific next-hop address. If a peer has no associated
// prefixes, or if no peers/paths exist, the policy is updated or cleared accordingly.
//
// The affectedOwners argument optionally scopes the outbound soft reset to peers whose owner
// matches one of the given owners. When empty, all peers are soft reset (e.g. after a full
// listener reconfiguration).
func (s *Server) updatePolicies(affectedOwners ...string) error {
	// Serialize policy rebuilds so that concurrent configuration changes cannot
	// interleave teardown and rebuild operations, and so a rebuild cannot race
	// the BGP listener lifecycle (see Configure).
	s.policyMu.Lock()
	defer s.policyMu.Unlock()

	return s.updatePoliciesLocked(affectedOwners...)
}

// updatePoliciesLocked rebuilds the export policy. Callers must hold policyMu.
//
// The new policy and its defined sets are built first (using a unique name and
// generation-suffixed set names so they never collide with the previously applied
// policy). The export assignment is then atomically swapped to the new policy, and
// only afterwards is the previous policy and its sets removed. This avoids the
// transient reject-all that a teardown-first approach would apply to all peers, and
// means a failure before the swap leaves the previous policies in place.
func (s *Server) updatePoliciesLocked(affectedOwners ...string) error {
	bgpServer, oldPolicyName, peers, paths := s.policySnapshot()
	if bgpServer == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Snapshot the sets to remove before building, so the new sets are never
	// mistaken for ones left behind by a previous rebuild.
	oldSets, err := listManagedSets(ctx, bgpServer)
	if err != nil {
		return err
	}

	var policy *bgpAPI.Policy
	var createdSets []*bgpAPI.DefinedSet
	if len(peers) > 0 && len(paths) > 0 {
		policy, createdSets, err = buildExportPolicy(ctx, bgpServer, peers, paths)
		if err != nil {
			// Clean up any sets created before the failure so they do not
			// accumulate across repeated failed rebuilds.
			deleteDefinedSets(ctx, bgpServer, createdSets)
			return err
		}
	}

	if policy != nil {
		err = bgpServer.AddPolicy(ctx, &bgpAPI.AddPolicyRequest{Policy: policy})
		if err != nil {
			// The new policy was not added, so its sets are unreferenced and can
			// be removed.
			deleteDefinedSets(ctx, bgpServer, createdSets)
			return fmt.Errorf("Failed adding BGP export policy: %w", err)
		}
	}

	// Atomically swap the export assignment to the new policy (or a reject-all
	// assignment when there is nothing to advertise). If this fails the previous
	// assignment remains in place.
	err = applyExportPolicy(ctx, bgpServer, policy)
	if err != nil {
		// The new policy (if any) is not referenced by the assignment, so it and
		// its sets can be removed. Attempt the policy delete first so its
		// statements no longer reference the sets.
		if policy != nil {
			err2 := bgpServer.DeletePolicy(ctx, &bgpAPI.DeletePolicyRequest{All: true, Policy: &bgpAPI.Policy{Name: policy.Name}})
			if err2 != nil {
				logger.Warn("Failed deleting unreferenced BGP export policy during cleanup", logger.Ctx{"policy": policy.Name, "err": err2})
			}
		}

		deleteDefinedSets(ctx, bgpServer, createdSets)
		return err
	}

	// The new assignment is in place, so the previous policy and its sets can be
	// safely removed.
	newPolicyName := ""
	if policy != nil {
		newPolicyName = policy.Name
	}

	deleteExportPolicy(ctx, bgpServer, oldPolicyName, oldSets)
	s.setPolicyName(newPolicyName)

	// Soft reset outbound route advertisements on affected peers so updated
	// export policies take effect immediately.
	softResetPeers(ctx, bgpServer, peers, affectedOwners)

	return nil
}
