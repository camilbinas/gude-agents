package cloudwatch

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	cw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	agent "github.com/camilbinas/gude-agents/agent"
)

const (
	defaultNamespace     = "GudeAgents"
	defaultFlushInterval = 60 * time.Second
	maxDataPerPut        = 1000 // CloudWatch PutMetricData limit
)

// cloudwatchClient abstracts the CloudWatch API for testability.
type cloudwatchClient interface {
	PutMetricData(ctx context.Context, params *cw.PutMetricDataInput, optFns ...func(*cw.Options)) (*cw.PutMetricDataOutput, error)
}

// cloudwatchHook implements agent.MetricsHook by buffering metric data points
// and flushing them to CloudWatch periodically.
type cloudwatchHook struct {
	client        cloudwatchClient
	namespace     string
	flushInterval time.Duration
	dimensions    []cwtypes.Dimension // extra user-supplied dimensions
	agentName     string              // optional agent name dimension

	mu     sync.Mutex
	buffer []cwtypes.MetricDatum

	stopCh chan struct{}
	doneCh chan struct{}
}

var _ agent.MetricsHook = (*cloudwatchHook)(nil)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func statusValue(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

func counterDatum(name string, value float64, dims ...cwtypes.Dimension) cwtypes.MetricDatum {
	now := aws.Time(time.Now())
	return cwtypes.MetricDatum{
		MetricName: aws.String(name),
		Value:      aws.Float64(value),
		Unit:       cwtypes.StandardUnitCount,
		Timestamp:  now,
		Dimensions: dims,
	}
}

func durationDatum(name string, seconds float64, dims ...cwtypes.Dimension) cwtypes.MetricDatum {
	now := aws.Time(time.Now())
	return cwtypes.MetricDatum{
		MetricName: aws.String(name),
		StatisticValues: &cwtypes.StatisticSet{
			SampleCount: aws.Float64(1),
			Sum:         aws.Float64(seconds),
			Minimum:     aws.Float64(seconds),
			Maximum:     aws.Float64(seconds),
		},
		Unit:       cwtypes.StandardUnitSeconds,
		Timestamp:  now,
		Dimensions: dims,
	}
}

func dim(name, value string) cwtypes.Dimension {
	return cwtypes.Dimension{Name: aws.String(name), Value: aws.String(value)}
}

// ---------------------------------------------------------------------------
// Buffer and flush
// ---------------------------------------------------------------------------

// append adds a metric datum to the buffer (thread-safe).
func (h *cloudwatchHook) append(datum cwtypes.MetricDatum) {
	// Attach extra dimensions.
	datum.Dimensions = append(datum.Dimensions, h.dimensions...)
	if h.agentName != "" {
		datum.Dimensions = append(datum.Dimensions, dim("AgentName", h.agentName))
	}
	h.mu.Lock()
	h.buffer = append(h.buffer, datum)
	h.mu.Unlock()
}

// flushLoop runs in a goroutine, flushing buffered data at the configured interval.
func (h *cloudwatchHook) flushLoop() {
	defer close(h.doneCh)
	ticker := time.NewTicker(h.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.flush(context.Background())
		case <-h.stopCh:
			return
		}
	}
}

// flush sends all buffered data points to CloudWatch, splitting into batches
// of maxDataPerPut. Failed batches are retained for the next flush.
func (h *cloudwatchHook) flush(ctx context.Context) {
	h.mu.Lock()
	data := h.buffer
	h.buffer = nil
	h.mu.Unlock()

	if len(data) == 0 {
		return
	}

	var retained []cwtypes.MetricDatum
	for i := 0; i < len(data); i += maxDataPerPut {
		end := i + maxDataPerPut
		if end > len(data) {
			end = len(data)
		}
		batch := data[i:end]
		_, err := h.client.PutMetricData(ctx, &cw.PutMetricDataInput{
			Namespace:  aws.String(h.namespace),
			MetricData: batch,
		})
		if err != nil {
			log.Printf("cloudwatch metrics: PutMetricData failed: %v", err)
			retained = append(retained, batch...)
		}
	}

	if len(retained) > 0 {
		h.mu.Lock()
		h.buffer = append(retained, h.buffer...)
		h.mu.Unlock()
	}
}

// Flush triggers an immediate flush of buffered data points.
func (h *cloudwatchHook) Flush(ctx context.Context) {
	h.flush(ctx)
}

// Shutdown stops the background flush goroutine and performs a final flush.
func (h *cloudwatchHook) Shutdown(ctx context.Context) error {
	close(h.stopCh)
	<-h.doneCh
	h.flush(ctx)
	return nil
}

// ---------------------------------------------------------------------------
// Hook methods
// ---------------------------------------------------------------------------

func (h *cloudwatchHook) OnInvokeStart() func(err error, usage agent.TokenUsage) {
	start := time.Now()
	return func(err error, usage agent.TokenUsage) {
		elapsed := time.Since(start).Seconds()
		h.append(durationDatum("AgentInvokeDuration", elapsed))
		h.append(counterDatum("AgentInvokeTotal", 1, dim("Status", statusValue(err))))
	}
}

func (h *cloudwatchHook) OnIterationStart() {
	h.append(counterDatum("AgentIterationTotal", 1))
}

func (h *cloudwatchHook) OnProviderCallStart(modelID string) func(err error, usage agent.TokenUsage) {
	if modelID == "" {
		modelID = "unknown"
	}
	start := time.Now()
	return func(err error, usage agent.TokenUsage) {
		elapsed := time.Since(start).Seconds()
		modelDim := dim("ModelId", modelID)
		h.append(durationDatum("AgentProviderCallDuration", elapsed))
		h.append(counterDatum("AgentProviderCallTotal", 1, modelDim, dim("Status", statusValue(err))))
		if err == nil {
			h.append(counterDatum("AgentProviderTokensTotal", float64(usage.InputTokens),
				modelDim, dim("Direction", "input")))
			h.append(counterDatum("AgentProviderTokensTotal", float64(usage.OutputTokens),
				modelDim, dim("Direction", "output")))
		}
	}
}

func (h *cloudwatchHook) OnToolStart(toolName string) func(err error) {
	start := time.Now()
	return func(err error) {
		elapsed := time.Since(start).Seconds()
		toolDim := dim("ToolName", toolName)
		h.append(durationDatum("AgentToolCallDuration", elapsed, toolDim))
		h.append(counterDatum("AgentToolCallTotal", 1, toolDim, dim("Status", statusValue(err))))
	}
}

func (h *cloudwatchHook) OnGuardrailComplete(direction string, blocked bool) {
	if blocked {
		h.append(counterDatum("AgentGuardrailBlockTotal", 1, dim("Direction", direction)))
	}
}

func (h *cloudwatchHook) OnImagesAttached(imageCount int) {
	h.append(counterDatum("AgentImagesAttachedTotal", float64(imageCount)))
}

func (h *cloudwatchHook) OnDocumentsAttached(docCount int) {
	h.append(counterDatum("AgentDocumentsAttachedTotal", float64(docCount)))
}

// ---------------------------------------------------------------------------
// Options
// ---------------------------------------------------------------------------

// Option configures the CloudWatch metrics hook.
type Option func(*cloudwatchHook)

// WithNamespace sets the CloudWatch namespace for all published metrics.
func WithNamespace(ns string) Option {
	return func(h *cloudwatchHook) { h.namespace = ns }
}

// WithClient sets a pre-configured CloudWatch client, bypassing the default
// credential chain initialization.
func WithClient(c *cw.Client) Option {
	return func(h *cloudwatchHook) { h.client = c }
}

// WithFlushInterval sets the time between flush cycles.
func WithFlushInterval(d time.Duration) Option {
	return func(h *cloudwatchHook) { h.flushInterval = d }
}

// WithDimensions adds extra key-value pairs as dimensions to all published metrics.
func WithDimensions(dims map[string]string) Option {
	return func(h *cloudwatchHook) {
		for k, v := range dims {
			h.dimensions = append(h.dimensions, cwtypes.Dimension{
				Name:  aws.String(k),
				Value: aws.String(v),
			})
		}
	}
}

// WithMetrics returns an agent.Option and a shutdown function.
// The shutdown function stops the background flush goroutine and performs
// a final flush of all buffered data points.
func WithMetrics(opts ...Option) (agent.Option, func(context.Context) error) {
	var hook *cloudwatchHook
	var shutdownFn func(context.Context) error

	agentOpt := func(a *agent.Agent) error {
		hook = &cloudwatchHook{
			namespace:     defaultNamespace,
			flushInterval: defaultFlushInterval,
			stopCh:        make(chan struct{}),
			doneCh:        make(chan struct{}),
		}
		for _, opt := range opts {
			opt(hook)
		}
		if hook.client == nil {
			cfg, err := awsconfig.LoadDefaultConfig(context.Background())
			if err != nil {
				return fmt.Errorf("cloudwatch metrics: load AWS config: %w", err)
			}
			hook.client = cw.NewFromConfig(cfg)
		}
		go hook.flushLoop()
		hook.agentName = a.Name()
		a.SetMetricsHook(hook)
		shutdownFn = hook.Shutdown
		return nil
	}

	return agent.Option(agentOpt), func(ctx context.Context) error {
		if shutdownFn != nil {
			return shutdownFn(ctx)
		}
		return nil
	}
}
