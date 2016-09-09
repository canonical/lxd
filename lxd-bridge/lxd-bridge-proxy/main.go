package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
)

func main() {
	addr := flag.String("addr", "[fe80::1%lxdbr0]:13128", "proxy listen address")
	flag.Parse()

	log.Fatal(http.ListenAndServe(*addr, &httputil.ReverseProxy{Director: func(req *http.Request) {}}))
}
