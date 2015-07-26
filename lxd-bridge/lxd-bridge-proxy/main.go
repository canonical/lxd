package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/elazarl/goproxy"
)

func main() {
	addr := flag.String("addr", "[fe80::1%lxdbr0]:3128", "proxy listen address")
	flag.Parse()

	proxy := goproxy.NewProxyHttpServer()
	log.Fatal(http.ListenAndServe(*addr, proxy))
}
