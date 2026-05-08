package graph

import (
	"fmt"
	"sort"
	"strings"
)

// validateDataFlow performs data-flow validation on the graph before execution.
// It checks for:
// 1. Output key uniqueness — no two nodes may declare the same output key
// 2. Input key satisfiability — every input key must be in initial state or produced by another node
// 3. Cycle detection — the data-flow dependency graph must be a DAG
//
// The entry node is special-cased: its input keys are NOT validated for satisfiability
// (it always runs first).
func (g *Graph[S]) validateDataFlow(initialKeys map[string]bool) error {
	// 1. Output key uniqueness: check that no two nodes declare the same output key.
	outputOwner := make(map[string]string) // key → node name
	for nodeName, meta := range g.dataflow {
		for _, key := range meta.OutputKeys {
			if existing, conflict := outputOwner[key]; conflict {
				// Sort the two node names for deterministic error message.
				a, b := existing, nodeName
				if a > b {
					a, b = b, a
				}
				return &GraphValidationError{
					Message: fmt.Sprintf("nodes %q and %q both declare output key %q", a, b, key),
				}
			}
			outputOwner[key] = nodeName
		}
	}

	// 2. Input key satisfiability: for each node (except entry), verify every declared
	// input key is either in the initial state keys or declared as an output key by another node.
	allOutputKeys := make(map[string]bool)
	for _, meta := range g.dataflow {
		for _, key := range meta.OutputKeys {
			allOutputKeys[key] = true
		}
	}

	for nodeName, meta := range g.dataflow {
		if nodeName == g.entry {
			continue // entry node is special-cased
		}
		for _, key := range meta.InputKeys {
			if !initialKeys[key] && !allOutputKeys[key] {
				return &GraphValidationError{
					Message: fmt.Sprintf("node %q: input key %q is not produced by any node and not in initial state", nodeName, key),
				}
			}
		}
	}

	// 3. Cycle detection using Kahn's algorithm (topological sort).
	// Build directed dependency graph: edge from producer to consumer.
	// An edge exists from node A to node B when B declares an input key that A declares as an output key.
	inDegree := make(map[string]int)
	adjacency := make(map[string][]string) // producer → list of consumers

	// Initialize all nodes.
	for nodeName := range g.dataflow {
		inDegree[nodeName] = 0
	}

	// Build edges.
	for consumerName, meta := range g.dataflow {
		for _, inputKey := range meta.InputKeys {
			if producerName, ok := outputOwner[inputKey]; ok && producerName != consumerName {
				adjacency[producerName] = append(adjacency[producerName], consumerName)
				inDegree[consumerName]++
			}
		}
	}

	// Kahn's algorithm: start with nodes that have in-degree 0.
	var queue []string
	for nodeName, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, nodeName)
		}
	}
	sort.Strings(queue) // deterministic processing order

	processed := 0
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		processed++

		for _, neighbor := range adjacency[node] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
				sort.Strings(queue) // maintain deterministic order
			}
		}
	}

	// If not all nodes were processed, there's a cycle.
	if processed < len(inDegree) {
		// Collect nodes that are part of the cycle (those with remaining in-degree > 0).
		var cycleNodes []string
		for nodeName, deg := range inDegree {
			if deg > 0 {
				cycleNodes = append(cycleNodes, nodeName)
			}
		}
		sort.Strings(cycleNodes)
		return &GraphValidationError{
			Message: fmt.Sprintf("data-flow cycle detected involving nodes: [%s]", strings.Join(cycleNodes, ", ")),
		}
	}

	return nil
}
