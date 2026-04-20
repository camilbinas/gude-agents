package cloudwatch

import (
	"context"
	"fmt"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	cw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"

	agent "github.com/camilbinas/gude-agents/agent"
)

// swarmCloudwatchHook implements agent.SwarmMetricsHook by buffering metric data
// points and flushing them to CloudWatch periodically.
type swarmCloudwatchHook struct {
	*cloudwatchHook // shared buffer, flush, and dimensions
}

var _ agent.SwarmMetricsHook = (*swarmCloudwatchHook)(nil)

func (h *swarmCloudwatchHook) OnSwarmRunStart() func(err error, result agent.SwarmResult) {
	start := time.Now()
	return func(err error, result agent.SwarmResult) {
		elapsed := time.Since(start).Seconds()
		h.append(durationDatum("SwarmRunDuration", elapsed))
		h.append(counterDatum("SwarmRunTotal", 1, dim("Status", statusValue(err))))
	}
}

func (h *swarmCloudwatchHook) OnSwarmAgentStart(agentName string) func(err error) {
	start := time.Now()
	return func(err error) {
		elapsed := time.Since(start).Seconds()
		nameDim := dim("AgentName", agentName)
		h.append(durationDatum("SwarmAgentTurnDuration", elapsed, nameDim))
		h.append(counterDatum("SwarmAgentTurnTotal", 1, nameDim, dim("Status", statusValue(err))))
	}
}

func (h *swarmCloudwatchHook) OnSwarmHandoff(from, to string) {
	h.append(counterDatum("SwarmHandoffTotal", 1, dim("From", from), dim("To", to)))
}

// WithSwarmMetrics returns an agent.SwarmOption and a shutdown function.
// The shutdown function stops the background flush goroutine and performs
// a final flush of all buffered data points.
func WithSwarmMetrics(opts ...Option) (agent.SwarmOption, func(context.Context) error) {
	var hook *swarmCloudwatchHook
	var shutdownFn func(context.Context) error

	swarmOpt := func(s *agent.Swarm) error {
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

		hook = &swarmCloudwatchHook{cloudwatchHook: inner}
		s.SetSwarmMetricsHook(hook)
		shutdownFn = inner.Shutdown
		return nil
	}

	return agent.SwarmOption(swarmOpt), func(ctx context.Context) error {
		if shutdownFn != nil {
			return shutdownFn(ctx)
		}
		return nil
	}
}
