global
  # HAProxy global settings
  log stdout local0 info
  chroot /var/lib/haproxy
  stats socket /run/haproxy/admin.sock mode 660 level admin
  stats timeout 30s
  user haproxy
  group haproxy
  daemon

defaults
  mode tcp
  log global
  option tcplog
  option dontlognull
  timeout connect 5s
  timeout client 30s
  timeout server 30s
  timeout check 10s
  maxconn 3000

# Frontend for HTTP traffic (port 80) - ACME challenges only
frontend http_frontend
  bind *:80
  mode http
  option httplog

  # Define ACLs
  acl known_hosts hdr(host) @@FQDN@@
  acl acme_challenge path_beg /.well-known/acme-challenge/

  # Only redirect ACME challenges for known hosts to HTTPS (LXD will handle them)
  http-request deny unless known_hosts
  http-request deny unless acme_challenge
  redirect scheme https code 301

# Frontend for HTTPS traffic (port 443) - TCP mode with SNI inspection
frontend https_frontend
  bind *:443

  # TCP request inspection for SNI and client filtering
  tcp-request inspect-delay 5s

  # Extract SNI from TLS handshake
  tcp-request content capture req.ssl_sni len 64

  # Define ACLs once for reuse
  acl tls_client_hello req_ssl_hello_type 1
  acl is_lxd_cluster req.ssl_sni @@FQDN@@
  # TLS 1.3 == SSL 3.4 but it is not possible to easily distinguish TLS 1.2 from 1.3
  # as TLS 1.3 tries to masquerade as a resumed TLS 1.2 connection to work around broken
  # middleboxes (see https://datatracker.ietf.org/doc/html/rfc8446#appendix-D.4).
  acl tls_too_old req.ssl_ver lt 3.3

  # Reject unwanted traffic
  # non-TLS
  tcp-request content reject unless tls_client_hello

  # for unknown SNI hosts
  tcp-request content reject unless is_lxd_cluster

  # using too old TLS version
  tcp-request content reject if tls_too_old

  # Rate limiting for LXD traffic (only for valid hosts that passed above checks)
  stick-table type ip size 100k expire 30s store conn_rate(10s)
  tcp-request content track-sc0 src
  tcp-request content reject if { sc_conn_rate(0) gt 20 }

  # Route to backend (all traffic reaching here is for LXD)
  default_backend lxd_cluster_tcp

# Additional frontend for LXD management on different port (optional)
frontend lxd_management
  bind *:8443
  mode tcp

  # Network restrictions
  acl mgmt_allowed_networks src 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 127.0.0.0/8
  tcp-request connection reject unless mgmt_allowed_networks

  default_backend lxd_cluster_tcp

# Backend for LXD cluster (TCP mode with TLS passthrough)
backend lxd_cluster_tcp
  mode tcp
  balance roundrobin

  # Sticky sessions based on TLS session ID (extracted from handshake)
  stick-table type binary len 32 size 30k expire 30m
  acl clienthello req_ssl_hello_type 1
  acl serverhello rep_ssl_hello_type 2
  # use tcp content accepts to detects ssl client and server hello.
  tcp-request inspect-delay 5s
  tcp-request content accept if clienthello
  # no timeout on response inspect delay by default.
  tcp-response content accept if serverhello
  # SSL session ID (SSLID) may be present on a client or server hello.
  # Its length is coded on 1 byte at offset 43 and its value starts
  # at offset 44.
  # Match and learn on request if client hello.
  stick on payload_lv(43,1) if clienthello
  # Learn on response if server hello.
  stick store-response payload_lv(43,1) if serverhello

  # Health checks using simple TCP connect
  option tcp-check

  # Failed connections will be redispatched to another cluster member
  option redispatch

  # LXD cluster members with proxy protocol and core.https_trusted_proxy
@@BACKEND_SERVERS@@
