package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
)

func NewProxy() *httputil.ReverseProxy {
	director := func(req *http.Request) {
		if req.Method == "CONNECT" {
			fmt.Printf("CONNECT: %s\n", req.Host)
		}
	}
	return &httputil.ReverseProxy{Director: director}
}

func main() {
	addr := flag.String("addr", "[fe80::1%lxdbr0]:3128", "proxy listen address")
	flag.Parse()

	log.Fatal(http.ListenAndServe(*addr, NewProxy()))
}
