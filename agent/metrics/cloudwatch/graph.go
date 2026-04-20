package cloudwatch

import (
	"context"
	"fmt"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	cw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"

	"github.com/camilbinas/gude-agents/agent/graph"
)

// graphCloudwatchHook implements graph.GraphMetricsHook by buffering metric data
// points and flushing them to CloudWatch periodically.
type graphCloudwatchHook struct {
	*cloudwatchHook // shared buffer, flush, and dimensions
}

var _ graph.GraphMetricsHook = (*graphCloudwatchHook)(nil)

func (h *graphCloudwatchHook) OnGraphRunStart() func(err error, iterations int) {
	start := time.Now()
	return func(err error, iterations int) {
		elapsed := time.Since(start).Seconds()
		h.append(durationDatum("GraphRunDuration", elapsed))
		h.append(counterDatum("GraphRunTotal", 1, dim("Status", statusValue(err))))
	}
}

func (h *graphCloudwatchHook) OnNodeStart(nodeName string) func(err error) {
	start := time.Now()
	return func(err error) {
		elapsed := time.Since(start).Seconds()
		nameDim := dim("NodeName", nodeName)
		h.append(durationDatum("GraphNodeDuration", elapsed, nameDim))
		h.append(counterDatum("GraphNodeTotal", 1, nameDim, dim("Status", statusValue(err))))
	}
}

// WithGraphMetrics returns a graph.GraphOption and a shutdown function.
// The shutdown function stops the background flush goroutine and performs
// a final flush of all buffered data points.
func WithGraphMetrics(opts ...Option) (graph.GraphOption, func(context.Context) error) {
	var hook *graphCloudwatchHook
	var shutdownFn func(context.Context) error

	graphOpt := func(g *graph.Graph) error {
		inner := &cloudwatchHook{
			namespace:     defaultNamespace,
			flushInterval: defaultFlushInterval,
			stopCh:        make(chan struct{}),
			doneCh:        make(chan struct{}),
		}
		for _, opt := range opts {
			opt(inner)
		}
		if inner.client == nil {
			cfg, err := awsconfig.LoadDefaultConfig(context.Background())
			if err != nil {
				return fmt.Errorf("cloudwatch metrics: load AWS config: %w", err)
			}
			inner.client = cw.NewFromConfig(cfg)
		}
		go inner.flushLoop()

		hook = &graphCloudwatchHook{cloudwatchHook: inner}
		g.SetGraphMetricsHook(hook)
		shutdownFn = inner.Shutdown
		return nil
	}

	return graph.GraphOption(graphOpt), func(ctx context.Context) error {
		if shutdownFn != nil {
			return shutdownFn(ctx)
		}
		return nil
	}
}
