package main

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/linuxkit/virtsock/pkg/vsock"
)

func main() {
	r := mux.NewRouter()
	r.HandleFunc("/state", stateHandler)
	http.Handle("/", r)

	l, err := vsock.Listen(vsock.CIDAny, 8443)
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()

	log.Fatal(http.Serve(l, nil))
}

func stateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(renderState())
}
