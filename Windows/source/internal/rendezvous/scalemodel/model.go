// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package scalemodel is a deterministic control-plane model, not a crypto or throughput simulation.
package scalemodel

import (
	"fmt"
	"math"
)

type Config struct {
	Exits         int
	Clients       int
	Servers       int
	Registrations int
	Candidates    int
	Progressive   bool
	PoolLabel     string
	Failure       float64
	Correlated    bool
	Seed          uint64
}

type Result struct {
	Config
	SuccessfulClients             int     `json:"successful_clients"`
	NoCandidateClients            int     `json:"no_candidate_clients"`
	CandidateChecks               int64   `json:"candidate_checks"`
	OwnerChecks                   int64   `json:"owner_checks"`
	LogicalGETs                   int64   `json:"logical_gets"`
	LogicalSAMPLEs                int64   `json:"logical_samples"`
	ApproxRegistrations           int64   `json:"approx_registrations"`
	AverageRegistrationsPerServer float64 `json:"average_registrations_per_server"`
	AverageHeartbeatsPerSecond    float64 `json:"average_heartbeats_per_second"`
	AverageGETsPerSecond          float64 `json:"average_gets_per_second"`
	AverageSAMPLEsPerSecond       float64 `json:"average_samples_per_second"`
}

func Run(config Config) (Result, error) {
	if config.Exits < 1 || config.Clients < 1 || config.Servers < 1 ||
		config.Registrations < 1 || config.Registrations > 4 ||
		config.Candidates < 1 || config.Candidates > 10 ||
		config.Progressive && config.Candidates != 10 ||
		config.Failure < 0 || config.Failure >= 1 || config.Seed == 0 {
		return Result{}, fmt.Errorf("invalid scale-model configuration")
	}
	owners := make([]uint32, config.Exits*config.Registrations)
	random := generator{state: config.Seed}
	for exit := 0; exit < config.Exits; exit++ {
		used := owners[exit*config.Registrations : (exit+1)*config.Registrations]
		for copy := range used {
			for {
				server := uint32(random.next() % uint64(config.Servers))
				if !contains(used[:copy], server) {
					used[copy] = server
					break
				}
			}
		}
	}
	failed := make([]bool, config.Servers)
	failureCount := int(math.Round(float64(config.Servers) * config.Failure))
	if config.Correlated {
		start := int(random.next() % uint64(config.Servers))
		for offset := 0; offset < failureCount; offset++ {
			failed[(start+offset)%config.Servers] = true
		}
	} else {
		for marked := 0; marked < failureCount; {
			index := int(random.next() % uint64(config.Servers))
			if !failed[index] {
				failed[index] = true
				marked++
			}
		}
	}
	registrations := int64(config.Exits * config.Registrations)
	result := Result{
		Config: config, ApproxRegistrations: registrations,
		AverageRegistrationsPerServer: float64(registrations) / float64(config.Servers),
		AverageHeartbeatsPerSecond:    float64(registrations) / float64(config.Servers) / 30,
	}
	for client := 0; client < config.Clients; client++ {
		found := false
		base := mix(config.Seed ^ uint64(client+1))
		result.LogicalGETs++
		result.LogicalSAMPLEs += 2
		for candidate := 0; candidate < config.Candidates; candidate++ {
			if config.Progressive && candidate == 5 {
				result.LogicalGETs++
				result.LogicalSAMPLEs += 2
			}
			exit := int(mix(base+uint64(candidate)*0x9e3779b97f4a7c15) % uint64(config.Exits))
			result.CandidateChecks++
			for _, owner := range owners[exit*config.Registrations : (exit+1)*config.Registrations] {
				result.OwnerChecks++
				if !failed[owner] {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if found {
			result.SuccessfulClients++
		} else {
			result.NoCandidateClients++
		}
	}
	result.AverageGETsPerSecond = float64(result.LogicalGETs) / 60 / float64(config.Servers)
	result.AverageSAMPLEsPerSecond = float64(result.LogicalSAMPLEs) / 60 / float64(config.Servers)
	return result, nil
}

type generator struct{ state uint64 }

func (g *generator) next() uint64 {
	g.state ^= g.state << 13
	g.state ^= g.state >> 7
	g.state ^= g.state << 17
	return g.state
}

func mix(value uint64) uint64 {
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func contains(values []uint32, wanted uint32) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
