package dns

import (
	"net"
	"testing"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/api"
)

// mockResponseWriter is a minimal dns.ResponseWriter for use in tests.
type mockResponseWriter struct {
	remoteAddr net.Addr
	tsigStatus error
	written    *dns.Msg
}

func (m *mockResponseWriter) LocalAddr() net.Addr         { return m.remoteAddr }
func (m *mockResponseWriter) RemoteAddr() net.Addr        { return m.remoteAddr }
func (m *mockResponseWriter) WriteMsg(msg *dns.Msg) error { m.written = msg; return nil }
func (m *mockResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (m *mockResponseWriter) Close() error                { return nil }
func (m *mockResponseWriter) TsigStatus() error           { return m.tsigStatus }
func (m *mockResponseWriter) TsigTimersOnly(bool)         {}
func (m *mockResponseWriter) Hijack()                     {}

// newMockWriter returns a mockResponseWriter whose remote address is addr (host:port).
func newMockWriter(addr string, tsigStatus error) *mockResponseWriter {
	tcpAddr, _ := net.ResolveTCPAddr("tcp", addr)
	return &mockResponseWriter{
		remoteAddr: tcpAddr,
		tsigStatus: tsigStatus,
	}
}

func TestWriteRcode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		rcode int
	}{
		{"SERVFAIL", dns.RcodeServerFailure},
		{"NXDOMAIN", dns.RcodeNameError},
		{"NOTIMP", dns.RcodeNotImplemented},
		{"FORMERR", dns.RcodeFormatError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := newMockWriter("127.0.0.1:12345", nil)
			r := new(dns.Msg)
			r.SetQuestion("example.net.", dns.TypeAXFR)

			writeRcode(w, r, tt.rcode)

			require.NotNil(t, w.written, "Expected a response message to be written.")
			assert.Equal(t, tt.rcode, w.written.Rcode)
		})
	}
}

func TestServeDNS_UnsupportedQueryType(t *testing.T) {
	t.Parallel()

	unsupported := []uint16{dns.TypeA, dns.TypeAAAA, dns.TypeMX, dns.TypeNS, dns.TypeTXT}

	for _, qtype := range unsupported {
		t.Run(dns.TypeToString[qtype], func(t *testing.T) {
			t.Parallel()

			s := &Server{zoneRetriever: func(name string, full bool) (*Zone, error) {
				return &Zone{}, nil
			}}
			h := &dnsHandler{server: s}
			w := newMockWriter("127.0.0.1:12345", nil)

			r := new(dns.Msg)
			r.SetQuestion("example.net.", qtype)

			h.ServeDNS(w, r)

			require.NotNil(t, w.written)
			assert.Equal(t, dns.RcodeNotImplemented, w.written.Rcode)
		})
	}
}

func TestServeDNS_NoZoneRetriever(t *testing.T) {
	t.Parallel()

	s := &Server{} // zoneRetriever is nil
	h := &dnsHandler{server: s}
	w := newMockWriter("127.0.0.1:12345", nil)

	r := new(dns.Msg)
	r.SetQuestion("example.net.", dns.TypeSOA)

	h.ServeDNS(w, r)

	require.NotNil(t, w.written)
	assert.Equal(t, dns.RcodeServerFailure, w.written.Rcode)
}

func TestServeDNS_MultipleQuestions(t *testing.T) {
	t.Parallel()

	s := &Server{zoneRetriever: func(name string, full bool) (*Zone, error) {
		return &Zone{}, nil
	}}
	h := &dnsHandler{server: s}
	w := newMockWriter("127.0.0.1:12345", nil)

	r := new(dns.Msg)
	r.Question = []dns.Question{
		{Name: "example.net.", Qtype: dns.TypeSOA, Qclass: dns.ClassINET},
		{Name: "other.net.", Qtype: dns.TypeSOA, Qclass: dns.ClassINET},
	}

	h.ServeDNS(w, r)

	require.NotNil(t, w.written)
	assert.Equal(t, dns.RcodeServerFailure, w.written.Rcode)
}

func TestServeDNS_ZoneNotFound(t *testing.T) {
	t.Parallel()

	s := &Server{zoneRetriever: func(name string, full bool) (*Zone, error) {
		return nil, assert.AnError
	}}
	h := &dnsHandler{server: s}

	// Peer is configured so the request would be allowed if the zone existed.
	w := newMockWriter("127.0.0.1:12345", nil)
	r := new(dns.Msg)
	r.SetQuestion("does-not-exist.example.net.", dns.TypeSOA)

	h.ServeDNS(w, r)

	require.NotNil(t, w.written)
	assert.Equal(t, dns.RcodeNameError, w.written.Rcode)
}

func TestServeDNS_NoPeerConfig(t *testing.T) {
	t.Parallel()

	zone := &Zone{
		Info: api.NetworkZone{
			Name:   "example.net",
			Config: map[string]string{},
		},
		Content: "example.net.\t300\tIN\tSOA\tns1.example.net. admin.example.net. 1 3600 900 604800 300\n",
	}

	s := &Server{zoneRetriever: func(name string, full bool) (*Zone, error) {
		return zone, nil
	}}
	h := &dnsHandler{server: s}
	w := newMockWriter("127.0.0.1:12345", nil)
	r := new(dns.Msg)
	r.SetQuestion("example.net.", dns.TypeSOA)

	h.ServeDNS(w, r)

	require.NotNil(t, w.written)
	// No peers configured: access must be denied (NXDOMAIN to avoid information leaks).
	assert.Equal(t, dns.RcodeNameError, w.written.Rcode)
}

func TestServeDNS_SOA_IPPeer(t *testing.T) {
	t.Parallel()

	zone := &Zone{
		Info: api.NetworkZone{
			Name: "example.net",
			Config: map[string]string{
				"peers.test.address": "127.0.0.1",
			},
		},
		Content: "example.net.\t300\tIN\tSOA\tns1.example.net. admin.example.net. 1 3600 900 604800 300\n",
	}

	s := &Server{zoneRetriever: func(name string, full bool) (*Zone, error) {
		return zone, nil
	}}
	h := &dnsHandler{server: s}
	w := newMockWriter("127.0.0.1:12345", nil)
	r := new(dns.Msg)
	r.SetQuestion("example.net.", dns.TypeSOA)

	h.ServeDNS(w, r)

	require.NotNil(t, w.written)
	assert.Equal(t, dns.RcodeSuccess, w.written.Rcode)
	assert.True(t, w.written.Authoritative)
	require.NotEmpty(t, w.written.Answer)
	assert.Equal(t, dns.TypeSOA, w.written.Answer[0].Header().Rrtype)
}

// TestIsAllowed exercises isAllowed for all combinations of address/key/TSIG.
func TestIsAllowed(t *testing.T) {
	t.Parallel()

	// A valid TSIG record whose key name follows the LXD convention: zoneName_peerName.
	validTSIG := &dns.TSIG{
		Hdr: dns.RR_Header{Name: "example.net_mypeer."},
	}

	wrongKeyNameTSIG := &dns.TSIG{
		Hdr: dns.RR_Header{Name: "other.net_mypeer."},
	}

	tests := []struct {
		name       string
		config     map[string]string
		ip         string
		tsig       *dns.TSIG
		tsigStatus bool
		wantAllow  bool
	}{
		{
			name:      "No peers configured: deny all",
			config:    map[string]string{},
			ip:        "127.0.0.1",
			wantAllow: false,
		},
		{
			name:      "IP peer: matching address allowed",
			config:    map[string]string{"peers.mypeer.address": "127.0.0.1"},
			ip:        "127.0.0.1",
			wantAllow: true,
		},
		{
			name:      "IP peer: wrong address denied",
			config:    map[string]string{"peers.mypeer.address": "127.0.0.2"},
			ip:        "127.0.0.1",
			wantAllow: false,
		},
		{
			name:      "Key peer: no TSIG denied",
			config:    map[string]string{"peers.mypeer.key": "secret"},
			ip:        "127.0.0.1",
			tsig:      nil,
			wantAllow: false,
		},
		{
			name:       "Key peer: invalid TSIG signature denied",
			config:     map[string]string{"peers.mypeer.key": "secret"},
			ip:         "127.0.0.1",
			tsig:       validTSIG,
			tsigStatus: false, // signature did not verify
			wantAllow:  false,
		},
		{
			name:       "Key peer: valid TSIG with wrong key name denied",
			config:     map[string]string{"peers.mypeer.key": "secret"},
			ip:         "127.0.0.1",
			tsig:       wrongKeyNameTSIG,
			tsigStatus: true,
			wantAllow:  false,
		},
		{
			name:       "Key peer: valid TSIG with correct key name allowed",
			config:     map[string]string{"peers.mypeer.key": "secret"},
			ip:         "127.0.0.1",
			tsig:       validTSIG,
			tsigStatus: true,
			wantAllow:  true,
		},
		{
			name: "Address+key peer: matching address and valid TSIG allowed",
			config: map[string]string{
				"peers.mypeer.address": "127.0.0.1",
				"peers.mypeer.key":     "secret",
			},
			ip:         "127.0.0.1",
			tsig:       validTSIG,
			tsigStatus: true,
			wantAllow:  true,
		},
		{
			name: "Address+key peer: wrong address with valid TSIG denied",
			config: map[string]string{
				"peers.mypeer.address": "127.0.0.2",
				"peers.mypeer.key":     "secret",
			},
			ip:         "127.0.0.1",
			tsig:       validTSIG,
			tsigStatus: true,
			wantAllow:  false,
		},
		{
			name: "Address+key peer: correct address without TSIG denied",
			config: map[string]string{
				"peers.mypeer.address": "127.0.0.1",
				"peers.mypeer.key":     "secret",
			},
			ip:        "127.0.0.1",
			tsig:      nil,
			wantAllow: false,
		},
		{
			name: "Cross-zone TSIG key: valid sig but wrong key name denied",
			config: map[string]string{
				"peers.mypeer.key": "secret",
			},
			ip:         "127.0.0.1",
			tsig:       wrongKeyNameTSIG,
			tsigStatus: true, // sig verified, but for a different zone's key
			wantAllow:  false,
		},
		{
			name:      "Malformed config key (missing field) ignored",
			config:    map[string]string{"peers.": "value"},
			ip:        "127.0.0.1",
			wantAllow: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := &dnsHandler{}
			zone := api.NetworkZone{
				Name:   "example.net",
				Config: tt.config,
			}

			got := h.isAllowed(zone, tt.ip, tt.tsig, tt.tsigStatus)
			assert.Equal(t, tt.wantAllow, got)
		})
	}
}
