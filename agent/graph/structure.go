package graph

// GraphStructure describes the topology of a graph for visualization and introspection.
type GraphStructure struct {
	Entry         string         `json:"entry"`
	Nodes         []NodeInfo     `json:"nodes"`
	DataFlowEdges []DataFlowEdge `json:"data_flow_edges"`
}

// NodeInfo describes a single node in the graph structure.
type NodeInfo struct {
	ID              string   `json:"id"`
	Label           string   `json:"label,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	Model           string   `json:"model,omitempty"`
	Tools           []string `json:"tools,omitempty"`
	InputKeys       []string `json:"input_keys,omitempty"`
	OutputKeys      []string `json:"output_keys,omitempty"`
	Layer           int      `json:"layer"`
	InterruptBefore bool     `json:"interrupt_before,omitempty"`
	InterruptAfter  bool     `json:"interrupt_after,omitempty"`
}

// Structure returns the graph's topology for visualization and introspection.
// Nodes are ordered by BFS distance from the entry node (left-to-right in a DAG layout).
// Safe to call after graph construction is complete.
func (g *Graph[S]) Structure() GraphStructure {
	gs := GraphStructure{Entry: g.entry}

	// BFS from entry to determine node order and layers.
	ordered, layers := g.bfsOrder()
	for _, name := range ordered {
		ni := NodeInfo{
			ID:              name,
			Layer:           layers[name],
			InterruptBefore: g.interruptBefore[name],
			InterruptAfter:  g.interruptAfter[name],
		}
		if meta, ok := g.nodeMeta[name]; ok {
			ni.Label = meta.Label
			ni.Provider = meta.Provider
			ni.Model = meta.Model
			ni.Tools = meta.Tools
		}
		// Dynamic tool resolution: if an agent is registered for this node,
		// read its current ToolSpecs() to reflect any runtime modifications.
		if a, ok := g.agentNodes[name]; ok {
			specs := a.ToolSpecs()
			toolNames := make([]string, len(specs))
			for i, s := range specs {
				toolNames[i] = s.Name
			}
			ni.Tools = toolNames
		}
		// Populate I/O metadata from data-flow declarations.
		if df, ok := g.dataflow[name]; ok {
			ni.InputKeys = df.InputKeys
			ni.OutputKeys = df.OutputKeys
		}
		gs.Nodes = append(gs.Nodes, ni)
	}

	// Build DataFlowEdges by matching consumer input keys to producer output keys.
	outputKeyToProducer := make(map[string]string, len(g.dataflow))
	for name, meta := range g.dataflow {
		for _, key := range meta.OutputKeys {
			outputKeyToProducer[key] = name
		}
	}

	edges := []DataFlowEdge{}
	for _, name := range ordered {
		meta, ok := g.dataflow[name]
		if !ok {
			continue
		}
		for _, inputKey := range meta.InputKeys {
			if producer, found := outputKeyToProducer[inputKey]; found {
				edges = append(edges, DataFlowEdge{
					From: producer,
					To:   name,
					Key:  inputKey,
				})
			}
		}
	}
	gs.DataFlowEdges = edges

	return gs
}

// bfsOrder returns node IDs in pipeline order with layer assignments for layout.
// Layers represent horizontal position in a left-to-right DAG visualization.
// Adjacency is built from data-flow declarations: producer → consumer when
// the producer's output key matches the consumer's input key.
func (g *Graph[S]) bfsOrder() ([]string, map[string]int) {
	adjacency, predecessors := g.buildDataFlowAdjacency()
	layers := g.computeLayers(predecessors)

	// BFS from entry for consistent node ordering in Structure output.
	visited := make(map[string]bool, len(g.nodes))
	var order []string

	if g.entry != "" {
		queue := []string{g.entry}
		visited[g.entry] = true
		order = append(order, g.entry)

		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]

			for _, neighbor := range adjacency[current] {
				if !visited[neighbor] {
					visited[neighbor] = true
					order = append(order, neighbor)
					queue = append(queue, neighbor)
				}
			}
		}
	}

	// Append any remaining nodes not reachable from entry.
	for name := range g.nodes {
		if !visited[name] {
			visited[name] = true
			order = append(order, name)
		}
	}

	return order, layers
}

// buildDataFlowAdjacency builds forward and reverse adjacency maps from data-flow
// declarations: producer → consumers and consumer → producers.
func (g *Graph[S]) buildDataFlowAdjacency() (adjacency, predecessors map[string][]string) {
	outputKeyToProducer := make(map[string]string, len(g.dataflow))
	for name, meta := range g.dataflow {
		for _, key := range meta.OutputKeys {
			outputKeyToProducer[key] = name
		}
	}

	adjacency = make(map[string][]string, len(g.nodes))
	predecessors = make(map[string][]string, len(g.nodes))
	for name, meta := range g.dataflow {
		for _, inputKey := range meta.InputKeys {
			if producer, found := outputKeyToProducer[inputKey]; found {
				adjacency[producer] = append(adjacency[producer], name)
				predecessors[name] = append(predecessors[name], producer)
			}
		}
	}
	return adjacency, predecessors
}

// computeLayers assigns each node a layer number based on the longest path from
// the entry node. A node's layer = max(predecessor layers) + 1, ensuring a node
// appears in the column of its latest dependency.
func (g *Graph[S]) computeLayers(predecessors map[string][]string) map[string]int {
	layers := make(map[string]int, len(g.nodes))
	computed := make(map[string]bool, len(g.nodes))

	var computeLayer func(name string) int
	computeLayer = func(name string) int {
		if computed[name] {
			return layers[name]
		}
		computed[name] = true
		if name == g.entry {
			layers[name] = 0
			return 0
		}
		maxPred := 0
		for _, pred := range predecessors[name] {
			if predLayer := computeLayer(pred) + 1; predLayer > maxPred {
				maxPred = predLayer
			}
		}
		// Nodes with no predecessors in the graph (inputs from initial state): layer 1.
		if len(predecessors[name]) == 0 && g.entry != "" {
			maxPred = 1
		}
		layers[name] = maxPred
		return maxPred
	}

	for name := range g.nodes {
		computeLayer(name)
	}
	return layers
}
