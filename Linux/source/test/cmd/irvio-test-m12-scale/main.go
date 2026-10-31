// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Irviodotnet/Irvio/internal/rendezvous/scalemodel"
)

func main() {
	exits := flag.Int("exits", 1_000_000, "logical Exit count")
	clients := flag.Int("clients", 1_000_000, "logical Client count")
	servers := flag.Int("servers", 1_000, "logical Rendezvous count")
	seed := flag.Uint64("seed", 0x485944524d3132, "deterministic workload seed")
	flag.Parse()
	encoder := json.NewEncoder(os.Stdout)
	pools := []struct {
		label string
		exits int
	}{{"common", *exits}, {"rare", min(*exits, 137)}}
	strategies := []struct {
		candidates  int
		progressive bool
	}{{5, false}, {10, false}, {10, true}}
	for _, pool := range pools {
		for _, registrations := range []int{1, 2, 3, 4} {
			for _, strategy := range strategies {
				for _, failure := range []float64{.1, .3, .5} {
					for _, correlated := range []bool{false, true} {
						result, err := scalemodel.Run(scalemodel.Config{
							Exits: pool.exits, Clients: *clients, Servers: *servers,
							Registrations: registrations, Candidates: strategy.candidates,
							Progressive: strategy.progressive, PoolLabel: pool.label,
							Failure: failure, Correlated: correlated, Seed: *seed,
						})
						if err != nil {
							fmt.Fprintln(os.Stderr, err)
							os.Exit(1)
						}
						if err := encoder.Encode(result); err != nil {
							fmt.Fprintln(os.Stderr, err)
							os.Exit(1)
						}
					}
				}
			}
		}
	}
}
