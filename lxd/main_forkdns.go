package main

import (
	"fmt"
	"strings"

	"github.com/miekg/dns"
	"github.com/spf13/cobra"
)

type cmdForkDNS struct {
	global *cmdGlobal
}

type dnsHandler struct {
	domain  string
	servers []string
}

func (h *dnsHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	msg := dns.Msg{}
	msg.SetReply(r)

	// We only support single questions for now
	if len(r.Question) != 1 {
		msg.SetRcode(r, dns.RcodeNameError)
		w.WriteMsg(&msg)
		return
	}

	// Rewrite the question to the internal domain
	origName := r.Question[0].Name
	newName := origName
	if strings.HasSuffix(r.Question[0].Name, fmt.Sprintf(".%s.", h.domain)) {
		newName = fmt.Sprintf("%s.__internal.", strings.SplitN(r.Question[0].Name, fmt.Sprintf(".%s.", h.domain), 2)[0])
	}

	// Query all the servers
	for _, server := range h.servers {
		// Send the request to the backend server
		r.Question[0].Name = newName
		resp, err := dns.Exchange(r, fmt.Sprintf("%s:53", server))
		r.Question[0].Name = origName
		if err != nil || len(resp.Answer) == 0 {
			// Error or empty response, try the next one
			continue
		}

		// Send back the answer
		answers := []dns.RR{}
		for _, answer := range resp.Answer {
			rr, err := dns.NewRR(strings.Replace(answer.String(), ".__internal.", fmt.Sprintf(".%s.", h.domain), -1))
			if err != nil {
				continue
			}

			answers = append(answers, rr)
		}
		msg.Answer = answers
		w.WriteMsg(&msg)
		return
	}

	// Fallback to NXDOMAIN
	msg.SetRcode(r, dns.RcodeNameError)
	w.WriteMsg(&msg)
}

func (c *cmdForkDNS) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkdns <listen address> <domain> <servers...>"
	cmd.Short = "Internal DNS proxy for clustering"
	cmd.Long = `Description:
  Spawns a tiny DNS server which forwards to all upstream servers until
  one returns a valid record.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkDNS) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	if len(args) < 3 {
		cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	srv := &dns.Server{
		Addr: args[0],
		Net:  "udp",
	}

	srv.Handler = &dnsHandler{
		domain:  args[1],
		servers: args[2:],
	}

	err := srv.ListenAndServe()
	if err != nil {
		return fmt.Errorf("Failed to set udp listener: %v\n", err)
	}

	return nil
}
