// forkdns provides a specialised DNS server designed for relaying A and PTR queries.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/miekg/dns"
	"github.com/spf13/cobra"
	"gopkg.in/fsnotify.v0"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/dnsutil"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
)

/*
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

extern char* advance_arg(bool required);

static int wait_for_pid(pid_t pid)
{
	int status, ret;

again:
	ret = waitpid(pid, &status, 0);
	if (ret == -1) {
		if (errno == EINTR)
			goto again;
		return -1;
	}
	if (ret != pid)
		goto again;
	if (!WIFEXITED(status) || WEXITSTATUS(status) != 0)
		return -1;
	return 0;
}

void forkdns()
{
	ssize_t ret;
	pid_t pid;
	FILE *pid_file;
	int log_fd;
	char *pid_path, *log_path;

	close(STDIN_FILENO);

	log_path = advance_arg(false);
	pid_path = advance_arg(false);

	// If arguments are missing, fall through to Go part without double forking to output usage info.
	if (log_path == NULL || pid_path == NULL)
		return;

	log_fd = open(log_path, O_WRONLY | O_CREAT | O_CLOEXEC | O_TRUNC, 0600);
	if (log_fd < 0)
		_exit(EXIT_FAILURE);

	ret = dup3(log_fd, STDOUT_FILENO, O_CLOEXEC);
	if (ret < 0)
		_exit(EXIT_FAILURE);

	ret = dup3(log_fd, STDERR_FILENO, O_CLOEXEC);
	if (ret < 0)
		_exit(EXIT_FAILURE);

	pid_file = fopen(pid_path, "we+");
	if (!pid_file) {
		fprintf(stderr,
			"%s - Failed to create pid file for forkdns daemon\n",
			strerror(errno));
		_exit(EXIT_FAILURE);
	}

	// daemonize
	pid = fork();
	if (pid < 0)
		_exit(EXIT_FAILURE);

	if (pid != 0) {
		ret = wait_for_pid(pid);
		if (ret < 0)
			_exit(EXIT_FAILURE);

		_exit(EXIT_SUCCESS);
	}

	pid = fork();
	if (pid < 0)
		_exit(EXIT_FAILURE);

	if (pid != 0) {
		ret = fprintf(pid_file, "%d", pid);
		fclose(pid_file);
		if (ret < 0) {
			fprintf(stderr, "Failed to write forkdns daemon pid %d to \"%s\"\n",
				pid, pid_path);
			ret = EXIT_FAILURE;
		}

		close(STDOUT_FILENO);
		close(STDERR_FILENO);
		_exit(EXIT_SUCCESS);
	}

	ret = setsid();
	if (ret < 0)
		fprintf(stderr, "%s - Failed to setsid in forkdns daemon\n",
			strerror(errno));
}
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
import "C"

type cmdForkDNS struct {
	global *cmdGlobal
}

type dnsHandler struct {
	domain    string
	leaseFile string
}

const forkdnsServersListPath = "forkdns.servers"
const forkdnsServersListFile = "servers.conf"

var dnsServersFileLock sync.Mutex
var dnsServersList []string

// serversFileMonitor performs an initial load of the server list and then waits for the file to be
// modified before triggering a reload.
func serversFileMonitor(watcher *fsnotify.Watcher, networkName string) {
	err := loadServersList(networkName)
	if err != nil {
		logger.Errorf("Server list load error: %v", err)
	}

	for {
		select {
		case ev := <-watcher.Event:
			// Ignore files events that dont concern the servers list file.
			if !strings.HasSuffix(ev.Name, forkdnsServersListPath+"/"+forkdnsServersListFile) {
				continue
			}
			err := loadServersList(networkName)
			if err != nil {
				logger.Errorf("Server list load error: %v", err)
				continue
			}
		case err := <-watcher.Error:
			logger.Errorf("Inotify error: %v", err)
		}
	}
}

// loadServersList reads the server list path and updates the internal servers list slice.
func loadServersList(networkName string) error {
	servers, err := networksGetForkdnsServersList(networkName)
	if err != nil {
		return err
	}

	// Safely apply new servers list to global list.
	dnsServersFileLock.Lock()
	dnsServersList = servers
	dnsServersFileLock.Unlock()
	logger.Infof("Server list loaded: %v", servers)
	return nil
}

// ServeDNS handles each DNS request.
func (h *dnsHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	var err error
	msg := dns.Msg{}
	msg.SetReply(r)

	// We only support single questions for now
	if len(r.Question) != 1 {
		msg.SetRcode(r, dns.RcodeNameError)
	} else if r.Question[0].Qtype == dns.TypePTR {
		msg, err = h.handlePTR(r)
		if err != nil {
			logger.Errorf("PTR record lookup failed for %s: %v", r.Question[0].Name, err)
			msg.SetRcode(r, dns.RcodeNameError)
		}
	} else if r.Question[0].Qtype == dns.TypeA {
		msg, err = h.handleA(r)
		if err != nil {
			logger.Errorf("A record lookup failed for %s: %v", r.Question[0].Name, err)
			msg.SetRcode(r, dns.RcodeNameError)
		}
	} else {
		// Fallback to NXDOMAIN
		msg.SetRcode(r, dns.RcodeNameError)
	}

	err = w.WriteMsg(&msg)
	if err != nil {
		logger.Errorf("Failed sending response for %s: %v", r.Question[0].Name, err)
	}
}

// handlePTR answers requests for reverse DNS records.
// It is used with cluster networking to provide cluster wide DNS PTR resolution by consulting the
// local DHCP leases file and if not found, then relaying the question to the other cluster member's
// forkdns instance. Returns DNS message to be sent as response.
func (h *dnsHandler) handlePTR(r *dns.Msg) (dns.Msg, error) {
	msg := dns.Msg{}
	msg.SetReply(r)

	// If request is marked as non-recursive it means it is from another forkdns and we should
	// attempt to answer it using the local dnsmasq lease file and not relay it.
	if !r.RecursionDesired {
		// Check if the local DHCP leases file contains a lease for the requested reverse DNS name.
		// If this fails with an error or no record found, then check other cluster hosts.
		hostname, err := h.getLeaseHostByReverseIPName(r.Question[0].Name)
		if err != nil {
			logger.Errorf("PTR record lease lookup failed for %s: %v", r.Question[0].Name, err)
		}

		// Record found in local DHCP leases file, generate answer response and send.
		if hostname != "" {
			msg.Authoritative = true
			msg.Answer = append(msg.Answer, &dns.PTR{
				Hdr: dns.RR_Header{
					Name:   r.Question[0].Name,
					Rrtype: dns.TypePTR,
					Class:  dns.ClassINET,
					Ttl:    0,
				},
				// Suffix the hostname in the lease with the cluster DNS zone name (e.g. ".lxd.")
				// The final full stop is important as the response needs to be a FQDN.
				Ptr: fmt.Sprintf("%s.%s.", hostname, h.domain),
			})

			return msg, nil
		}

		// Record not found locally, return NXDOMAIN.
		msg.SetRcode(r, dns.RcodeNameError)
		return msg, nil
	}

	// If we get here, then the recursion desired flag was set, meaning we cannot answer the
	// query locally and need to relay it to the other forkdns instances.

	// This tells the remote node we only want to query their local data (to stop loops).
	r.RecursionDesired = false

	// Get current list of servers safely.
	dnsServersFileLock.Lock()
	servers := dnsServersList
	dnsServersFileLock.Unlock()

	// Query all the servers.
	for _, server := range servers {
		resp, err := dns.Exchange(r, fmt.Sprintf("%s:1053", server))
		if err != nil || len(resp.Answer) == 0 {
			// Error or empty response, try the next one
			continue
		}

		return *resp, nil
	}

	// Record not found in any of the remove servers.
	msg.SetRcode(r, dns.RcodeNameError)
	return msg, nil
}

// getLeaseHostByReverseIPName finds the hostname used in the DHCP lease by supplying a reverse
// DNS hostname of the device's IP.
func (h *dnsHandler) getLeaseHostByReverseIPName(reverseName string) (string, error) {
	ip := dnsutil.ExtractAddressFromReverse(reverseName)
	if ip == "" {
		return "", errors.New("Failed to convert reverse name to IP")
	}

	file, err := os.Open(h.leaseFile)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 5 {
			address := fields[2]
			if address == ip {
				return fields[3], nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}

	return "", nil
}

// handleA answers requests for A DNS records.
// It is used with cluster networking to provide cluster wide DNS A resolution by consulting the
// local DHCP leases file and if not found, then relaying the question to the other cluster member's
// forkdns instance. Returns DNS message to be sent as response.
func (h *dnsHandler) handleA(r *dns.Msg) (dns.Msg, error) {
	msg := dns.Msg{}
	msg.SetReply(r)

	// If request is marked as non-recursive it means it is from another forkdns and we should
	// attempt to answer it using the local dnsmasq lease file and not relay it.
	if !r.RecursionDesired {
		// Check if the local DHCP leases file contains a lease for the requested hostname name.
		// If this fails with an error or no record found, then check other cluster hosts.
		ip, err := h.getLeaseHostByDNSName(r.Question[0].Name)
		if err != nil {
			logger.Errorf("A record lease lookup failed for %s: %v", r.Question[0].Name, err)
		}

		// Record found in local DHCP leases file, generate answer response and send.
		if ip != "" {
			msg.Authoritative = true
			msg.Answer = append(msg.Answer, &dns.A{
				Hdr: dns.RR_Header{
					Name:   r.Question[0].Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    0,
				},
				A: net.ParseIP(ip),
			})

			return msg, nil
		}

		// Record not found locally, return NXDOMAIN.
		msg.SetRcode(r, dns.RcodeNameError)
		return msg, nil
	}

	// If we get here, then the recursion desired flag was set, meaning we cannot answer the
	// query locally and need to relay it to the other forkdns instances.

	// This tells the remote node we only want to query their local data (to stop loops).
	r.RecursionDesired = false

	// Get current list of servers safely.
	dnsServersFileLock.Lock()
	servers := dnsServersList
	dnsServersFileLock.Unlock()

	// Query all the servers.
	for _, server := range servers {
		resp, err := dns.Exchange(r, fmt.Sprintf("%s:1053", server))
		if err != nil || len(resp.Answer) == 0 {
			// Error or empty response, try the next one
			continue
		}

		return *resp, nil
	}

	// Record not found in any of the remove servers.
	msg.SetRcode(r, dns.RcodeNameError)
	return msg, nil
}

// getLeaseHostByDNSName finds the hostname used in the DHCP lease by supplying a DNS A name
func (h *dnsHandler) getLeaseHostByDNSName(dnsName string) (string, error) {
	host := strings.TrimSuffix(dnsName, fmt.Sprintf(".%s.", h.domain))

	file, err := os.Open(h.leaseFile)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 5 {
			hostname := fields[3]
			if hostname == host {
				return fields[2], nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}

	return "", nil
}

func (c *cmdForkDNS) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkdns <pid path> <listen address> <domain> <network name>"
	cmd.Short = "Internal DNS proxy for clustering"
	cmd.Long = `Description:
  Spawns a specialised DNS server designed for relaying A and PTR queries that cannot be answered by
  the local dnsmasq process to the other cluster member's forkdns process where it will inspect the
  local dnsmasq lease file looking for an answer to the query.
  It uses the "recursion desired" flag in incoming DNS requests to modify its behaviour.
  When "recursion desired" is set to yes, the query is immediately relayed to the other cluster nodes
  (with the "recursion desired" flag set to no) as it indicates that the local dnsmasq process was
  unable to answer it from the local lease file.
  When "recursion desired" flag is set to no, this indicates the request has been sent from another
  forkdns process, and the local dnsmasq lease file only is parsed to try and answer the query.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkDNS) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	if len(args) < 5 {
		cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	log, err := logging.GetLogger("lxd-forkdns", "", false, false, nil)
	if err != nil {
		return err
	}
	logger.Log = log

	// Setup watcher on servers file.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("Unable to setup fsnotify: %s", err)
	}

	networkName := args[4]
	path := shared.VarPath("networks", networkName, forkdnsServersListPath)
	err = watcher.Watch(path)
	if err != nil {
		return fmt.Errorf("Unable to setup fsnotify watch on %s: %s", path, err)
	}

	os.Stdin.Close()
	os.Stderr.Close()
	os.Stdout.Close()

	// Run the server list monitor concurrently waiting for file changes.
	go serversFileMonitor(watcher, networkName)

	logger.Info("Started")

	srv := &dns.Server{
		Addr: args[2],
		Net:  "udp",
	}

	srv.Handler = &dnsHandler{
		domain:    args[3],
		leaseFile: shared.VarPath("networks", networkName, "dnsmasq.leases"),
	}

	err = srv.ListenAndServe()
	if err != nil {
		return fmt.Errorf("Failed to set udp listener: %v\n", err)
	}

	return nil
}
