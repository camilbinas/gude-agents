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

// cloudwatchHook implements the observer capabilities needed for metrics by
// buffering metric data points and flushing them to CloudWatch periodically.
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

var (
	_ agent.InvokeObserver     = (*cloudwatchHook)(nil)
	_ agent.IterationObserver  = (*cloudwatchHook)(nil)
	_ agent.ModelObserver      = (*cloudwatchHook)(nil)
	_ agent.ToolObserver       = (*cloudwatchHook)(nil)
	_ agent.GuardrailObserver  = (*cloudwatchHook)(nil)
	_ agent.AttachmentObserver = (*cloudwatchHook)(nil)
)

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
// Observer methods
// ---------------------------------------------------------------------------

func (h *cloudwatchHook) ObserveInvoke(ctx context.Context, record agent.InvokeRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	h.append(durationDatum("AgentInvokeDuration", record.Duration.Seconds()))
	h.append(counterDatum("AgentInvokeTotal", 1, dim("Status", statusValue(record.Err))))
	return ctx
}

func (h *cloudwatchHook) ObserveIteration(ctx context.Context, record agent.IterationRecord) context.Context {
	if record.Phase == agent.Start {
		h.append(counterDatum("AgentIterationTotal", 1))
	}
	return ctx
}

func (h *cloudwatchHook) ObserveModel(ctx context.Context, record agent.ModelCallRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	modelID := record.ModelID
	if modelID == "" {
		modelID = "unknown"
	}
	modelDim := dim("ModelId", modelID)
	h.append(durationDatum("AgentProviderCallDuration", record.Duration.Seconds()))
	h.append(counterDatum("AgentProviderCallTotal", 1, modelDim, dim("Status", statusValue(record.Err))))
	if record.Err == nil {
		h.append(counterDatum("AgentProviderTokensTotal", float64(record.Usage.InputTokens),
			modelDim, dim("Direction", "input")))
		h.append(counterDatum("AgentProviderTokensTotal", float64(record.Usage.OutputTokens),
			modelDim, dim("Direction", "output")))
		if record.Usage.CacheReadTokens > 0 {
			h.append(counterDatum("AgentProviderTokensTotal", float64(record.Usage.CacheReadTokens),
				modelDim, dim("Direction", "cache_read")))
		}
		if record.Usage.CacheWriteTokens > 0 {
			h.append(counterDatum("AgentProviderTokensTotal", float64(record.Usage.CacheWriteTokens),
				modelDim, dim("Direction", "cache_write")))
		}
	}
	return ctx
}

func (h *cloudwatchHook) ObserveTool(ctx context.Context, record agent.ToolCallRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	toolDim := dim("ToolName", record.Name)
	h.append(durationDatum("AgentToolCallDuration", record.Duration.Seconds(), toolDim))
	h.append(counterDatum("AgentToolCallTotal", 1, toolDim, dim("Status", statusValue(record.Err))))
	return ctx
}

func (h *cloudwatchHook) ObserveGuardrail(ctx context.Context, record agent.GuardrailRecord) context.Context {
	if record.Phase == agent.End && record.Blocked {
		h.append(counterDatum("AgentGuardrailBlockTotal", 1, dim("Direction", record.Direction)))
	}
	return ctx
}

func (h *cloudwatchHook) ObserveAttachment(ctx context.Context, record agent.AttachmentRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	if record.ImageCount > 0 {
		h.append(counterDatum("AgentImagesAttachedTotal", float64(record.ImageCount)))
	}
	if record.DocumentCount > 0 {
		h.append(counterDatum("AgentDocumentsAttachedTotal", float64(record.DocumentCount)))
	}
	return ctx
}

// ---------------------------------------------------------------------------
// Options
// ---------------------------------------------------------------------------

// Option configures the CloudWatch metrics observer.
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
		hook.agentName = a.Name()
		if err := agent.WithObserver(hook)(a); err != nil {
			return err
		}
		go hook.flushLoop()
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
