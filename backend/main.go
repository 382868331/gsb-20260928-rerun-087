package main

import (
	"log"
	"net/http"
)

func main() {
	store := NewStore("workflow-data.json")
	if err := store.Load(); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	RegisterRoutes(mux, store)

	log.Println("workflow server listening on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
