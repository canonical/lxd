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
	// policyMu serializes GoBGP export policy rebuilds so that concurrent
	// configuration changes cannot interleave teardown and rebuild operations.
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
	count    int
	owner    string
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

	// Spawn the BGP goroutines.
	s.bgp = bgpServer.NewBgpServer()
	go s.bgp.Serve()

	// Get the address and port.
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
		return err
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

	// Start the listener.
	err = s.bgp.StartBgp(context.Background(), &bgpAPI.StartBgpRequest{Global: conf})
	if err != nil {
		return err
	}

	// Copy the path list
	oldPaths := map[uuid.UUID]path{}
	maps.Copy(oldPaths, s.paths)

	// Add existing paths.
	s.paths = map[uuid.UUID]path{}
	for _, path := range oldPaths {
		err := s.addPrefix(path.prefix, path.nexthop, path.owner)
		if err != nil {
			logger.Warn("Cannot add prefix to BGP server", logger.Ctx{"prefix": path.prefix.String(), "err": err})
		}
	}

	// Copy the peer list.
	oldPeers := map[string]peer{}
	maps.Copy(oldPeers, s.peers)

	// Add existing peers.
	s.peers = map[string]peer{}
	for _, peer := range oldPeers {
		err := s.addPeer(peer.address, peer.asn, peer.password, peer.holdtime, peer.owner)
		if err != nil {
			return err
		}
	}

	// Record the address.
	s.address = address
	s.asn = asn
	s.routerID = routerID

	return nil
}

// Stop tears down the BGP listener.
func (s *Server) stop() error {
	// Skip if no instance.
	if s.bgp == nil {
		return nil
	}

	// Save the peer list.
	oldPeers := map[string]peer{}
	maps.Copy(oldPeers, s.peers)

	// Remove all the peers.
	for _, peer := range s.peers {
		err := s.removePeer(peer.address)
		if err != nil {
			return err
		}
	}

	// Restore peer list.
	s.peers = oldPeers

	// Stop the listener.
	err := s.bgp.StopBgp(context.Background(), &bgpAPI.StopBgpRequest{})
	if err != nil {
		return err
	}

	// Mark the daemon as down.
	s.address = ""
	s.asn = 0
	s.routerID = nil
	s.bgp = nil
	s.policyName = ""

	return nil
}

// Configure updates the listener with a new configuration..
func (s *Server) Configure(address string, asn uint32, routerID net.IP) error {
	// Locking.
	s.mu.Lock()
	err := s.configure(address, asn, routerID)
	s.mu.Unlock()
	if err != nil {
		return err
	}

	return s.updatePolicies()
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

// RemovePrefixByOwner removes all prefixes for the provided owner.
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

	// Look for an existing peer.
	bgpPeer, bgpPeerExists := s.peers[addrStr]
	if bgpPeerExists {
		if bgpPeer.asn != asn {
			return fmt.Errorf("Peer %q already used but with differing ASN (%d vs %d)", addrStr, asn, bgpPeer.asn)
		}

		if subtle.ConstantTimeCompare([]byte(bgpPeer.password), []byte(password)) != 1 {
			return fmt.Errorf("Peer %q already used but with a different password", addrStr)
		}

		if bgpPeer.owner != owner {
			return fmt.Errorf("Peer %q already used by owner %q (requested by %q)", addrStr, bgpPeer.owner, owner)
		}

		// Re-use the existing entry.
		bgpPeer.count++
		s.peers[addrStr] = bgpPeer
		return nil
	}

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

	// Add the peer to the list.
	s.peers[addrStr] = peer{
		address:  address,
		asn:      asn,
		password: password,
		holdtime: holdTime,
		count:    1,
		owner:    owner,
	}

	return nil
}

// RemovePeer removes a prefix from the BGP server.
func (s *Server) RemovePeer(address net.IP) error {
	// Locking.
	s.mu.Lock()
	err := s.removePeer(address)
	s.mu.Unlock()
	if err != nil {
		return err
	}

	return s.updatePolicies()
}

func (s *Server) removePeer(address net.IP) error {
	addrStr := address.String()

	// Find the peer.
	bgpPeer, bgpPeerExists := s.peers[addrStr]
	if !bgpPeerExists {
		return ErrPeerNotFound
	}

	// Remove the peer from the BGP server.
	if s.bgp != nil && bgpPeer.count == 1 {
		err := s.bgp.DeletePeer(context.Background(), &bgpAPI.DeletePeerRequest{Address: addrStr})
		if err != nil {
			return err
		}
	}

	// Update peer list.
	if bgpPeer.count == 1 {
		// Delete the peer.
		delete(s.peers, addrStr)
	} else {
		// Decrease refcount.
		bgpPeer.count--
		s.peers[addrStr] = bgpPeer
	}

	return nil
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
	// interleave teardown and rebuild operations. GoBGP API calls are still made
	// outside s.mu so that concurrent BGP configuration changes are not blocked.
	s.policyMu.Lock()
	defer s.policyMu.Unlock()

	// Snapshot the state needed to build the policy under lock, then release it
	// before making potentially slow GoBGP API calls.
	s.mu.Lock()
	bgpServer := s.bgp
	peers := make(map[string]peer, len(s.peers))
	maps.Copy(peers, s.peers)
	paths := make(map[uuid.UUID]path, len(s.paths))
	maps.Copy(paths, s.paths)
	s.mu.Unlock()

	if bgpServer == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Apply a fail-safe reject-all export assignment before touching the
	// previously applied policy. If any later step fails, no routes can be
	// unintentionally advertised instead of being left without export controls.
	err := bgpServer.SetPolicyAssignment(ctx, &bgpAPI.SetPolicyAssignmentRequest{
		Assignment: &bgpAPI.PolicyAssignment{
			Direction:     bgpAPI.PolicyDirection_POLICY_DIRECTION_EXPORT,
			DefaultAction: bgpAPI.RouteAction_ROUTE_ACTION_REJECT,
		},
	})
	if err != nil {
		return fmt.Errorf("Failed applying fail-safe BGP export policy: %w", err)
	}

	// 2. Delete the previously applied LXD export policy (if any) with All:true
	// so its statements are removed too. Its name is tracked so that a partially
	// failed previous rebuild cannot leave stale statements behind shadowing the
	// new ones. Errors are tolerated: the policy may already be gone (e.g. after
	// a listener restart).
	if s.policyName != "" {
		err = bgpServer.DeletePolicy(ctx, &bgpAPI.DeletePolicyRequest{
			All:    true,
			Policy: &bgpAPI.Policy{Name: s.policyName},
		})
		if err != nil {
			logger.Warn("Failed deleting previous BGP export policy", logger.Ctx{"policy": s.policyName, "err": err})
		}

		s.policyName = ""
	}

	// 3. Delete LXD-managed defined sets individually: DeleteDefinedSet requires
	// a named set, so an empty request fails and would leave stale sets behind.
	// GoBGP requires DefinedType to be specified when listing defined sets.
	for _, dt := range []bgpAPI.DefinedType{
		bgpAPI.DefinedType_DEFINED_TYPE_PREFIX,
		bgpAPI.DefinedType_DEFINED_TYPE_NEIGHBOR,
	} {
		var definedSets []*bgpAPI.DefinedSet
		err := bgpServer.ListDefinedSet(ctx, &bgpAPI.ListDefinedSetRequest{DefinedType: dt}, func(ds *bgpAPI.DefinedSet) {
			if strings.HasPrefix(ds.Name, "ps_peer_") || strings.HasPrefix(ds.Name, "ns_peer_") {
				definedSets = append(definedSets, ds)
			}
		})
		if err != nil {
			return fmt.Errorf("Failed listing defined sets of type %v: %w", dt, err)
		}

		for _, ds := range definedSets {
			err = bgpServer.DeleteDefinedSet(ctx, &bgpAPI.DeleteDefinedSetRequest{
				All:        true,
				DefinedSet: ds,
			})
			if err != nil {
				logger.Warn("Failed deleting defined set", logger.Ctx{"set": ds.Name, "err": err})
			}
		}
	}

	// If there are no peers or no paths, nothing further to install. The
	// fail-safe reject-all assignment remains applied.
	if len(peers) == 0 || len(paths) == 0 {
		return nil
	}

	// 4. Group paths by owner.
	pathsByOwner := map[string][]path{}
	for _, p := range paths {
		pathsByOwner[p.owner] = append(pathsByOwner[p.owner], p)
	}

	statements := make([]*bgpAPI.Statement, 0)
	var stmtIdx int

	// Generate a unique generation suffix so that neighbor set names cannot
	// collide with sets left behind by a partially failed previous rebuild.
	generation := uuid.New().String()

	for _, peer := range peers {
		addrStr := peer.address.String()
		peerTag := strings.ReplaceAll(strings.ReplaceAll(addrStr, ":", "_"), ".", "_")
		nsName := fmt.Sprintf("ns_peer_%s_%s", peerTag, generation)

		// Create NeighborSet for this peer (using /32 for IPv4, /128 for IPv6).
		peerCIDR := addrStr + "/32"
		if peer.address.To4() == nil {
			peerCIDR = addrStr + "/128"
		}

		err := bgpServer.AddDefinedSet(ctx, &bgpAPI.AddDefinedSetRequest{
			DefinedSet: &bgpAPI.DefinedSet{
				DefinedType: bgpAPI.DefinedType_DEFINED_TYPE_NEIGHBOR,
				Name:        nsName,
				List:        []string{peerCIDR},
			},
		})
		if err != nil {
			return fmt.Errorf("Failed adding neighbor defined set for peer %q: %w", addrStr, err)
		}

		// Find all paths matching this peer's owner (e.g., exact owner "network_1",
		// or related owners like "network_1_forward", "network_1_load_balancer", or instances).
		matchedPaths := make([]path, 0)
		for owner, paths := range pathsByOwner {
			if owner == peer.owner || strings.HasPrefix(owner, peer.owner+"_") {
				matchedPaths = append(matchedPaths, paths...)
			}
		}

		if len(matchedPaths) > 0 {
			// For each path or group of paths with the same next hop, create a prefix set and statement.
			pathsByNexthop := map[string][]path{}
			for _, p := range matchedPaths {
				nhStr := p.nexthop.String()
				pathsByNexthop[nhStr] = append(pathsByNexthop[nhStr], p)
			}

			for nhStr, nexthopPaths := range pathsByNexthop {
				stmtIdx++
				tag := uuid.New().String()
				psName := fmt.Sprintf("ps_peer_%s_%s", peerTag, tag)

				prefixes := make([]*bgpAPI.Prefix, 0, len(nexthopPaths))
				for _, p := range nexthopPaths {
					ones, _ := p.prefix.Mask.Size()
					prefixes = append(prefixes, &bgpAPI.Prefix{
						IpPrefix:      p.prefix.String(),
						MaskLengthMin: uint32(ones),
						MaskLengthMax: uint32(ones),
					})
				}

				err = bgpServer.AddDefinedSet(ctx, &bgpAPI.AddDefinedSetRequest{
					DefinedSet: &bgpAPI.DefinedSet{
						DefinedType: bgpAPI.DefinedType_DEFINED_TYPE_PREFIX,
						Name:        psName,
						Prefixes:    prefixes,
					},
				})
				if err != nil {
					return fmt.Errorf("Failed adding prefix defined set %q for peer %q: %w", psName, addrStr, err)
				}

				stmtName := fmt.Sprintf("stmt_accept_%s_%s", peerTag, tag)
				stmt := &bgpAPI.Statement{
					Name: stmtName,
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

				statements = append(statements, stmt)
			}
		}

		// Reject any other route advertisements toward this peer.
		stmtIdx++
		stmtRejectName := fmt.Sprintf("stmt_reject_%s_%s", peerTag, uuid.New().String())
		stmtReject := &bgpAPI.Statement{
			Name: stmtRejectName,
			Conditions: &bgpAPI.Conditions{
				NeighborSet: &bgpAPI.MatchSet{
					Type: bgpAPI.MatchSet_TYPE_ANY,
					Name: nsName,
				},
			},
			Actions: &bgpAPI.Actions{
				RouteAction: bgpAPI.RouteAction_ROUTE_ACTION_REJECT,
			},
		}

		statements = append(statements, stmtReject)
	}

	if len(statements) == 0 {
		return nil
	}

	// 5. Create Policy with a unique name so it never collides with the policy
	// that was previously applied (which is only deleted after the new one is
	// assigned).
	policy := &bgpAPI.Policy{
		Name:       "pol_lxd_export_" + generation,
		Statements: statements,
	}

	err = bgpServer.AddPolicy(ctx, &bgpAPI.AddPolicyRequest{Policy: policy})
	if err != nil {
		return fmt.Errorf("Failed adding BGP export policy: %w", err)
	}

	// 6. Atomically swap the export assignment to the new policy (replacing the
	// fail-safe reject-all or the previously applied policy), enforcing the
	// reject default so only matching routes are exported.
	err = bgpServer.SetPolicyAssignment(ctx, &bgpAPI.SetPolicyAssignmentRequest{
		Assignment: &bgpAPI.PolicyAssignment{
			Direction:     bgpAPI.PolicyDirection_POLICY_DIRECTION_EXPORT,
			DefaultAction: bgpAPI.RouteAction_ROUTE_ACTION_REJECT,
			Policies:      []*bgpAPI.Policy{policy},
		},
	})
	if err != nil {
		return fmt.Errorf("Failed assigning BGP export policy: %w", err)
	}

	// Record the policy name so it can be cleaned up on the next update (the
	// fail-safe reject-all assignment applied earlier replaced any previous
	// policy, so it is no longer in use and can be deleted).
	s.policyName = policy.Name

	// 7. Soft reset outbound route advertisements on affected peers so updated export
	// policies take effect immediately. When no owners are specified, reset all peers.
	for _, peer := range peers {
		if len(affectedOwners) > 0 {
			affected := false
			for _, owner := range affectedOwners {
				if peer.owner == owner || strings.HasPrefix(peer.owner, owner+"_") || strings.HasPrefix(owner, peer.owner+"_") {
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

	return nil
}
