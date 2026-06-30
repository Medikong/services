package main

import (
	"flag"
	"log"
	"net/http"

	passwordhash "medikong-auth-passwordhash-benchmark"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18081", "HTTP listen address")
	flag.Parse()

	server := &http.Server{
		Addr:    *addr,
		Handler: passwordhash.NewMux(),
	}
	log.Printf("password benchmark server listening on %s", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
