package main

import "github.com/abhijeet/fluxrouter/internal/server"

// runServe delegates to the server package Run loop.
func runServe(args []string) int {
	return server.Run(args, VERSION)
}