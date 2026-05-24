package agentcore

import (
	"net/url"
	"strings"
)

// AWS baggage keys propagated by the AgentCore Gateway during A/B testing.
const (
	baggageBundleARNKey     = "aws.agentcore.configbundle_arn"
	baggageBundleVersionKey = "aws.agentcore.configbundle_version"

	// Experiment baggage keys — used by the BaggageSpanProcessor to stamp
	// spans with the A/B test experiment identity so the online evaluation
	// pipeline can correlate sessions to variants.
	baggageExperimentARNKey = "aws.bedrock.agentcore.experimentArn"
	baggageVariantNameKey   = "aws.bedrock.agentcore.variantName"
)

// parseBundleRefFromBaggage extracts the AgentCore configuration bundle
// reference from a W3C baggage header value. Returns a zero BundleRef when
// the header is empty or does not carry both keys.
//
// The W3C Baggage format is:
//
//	key1=value1[;property], key2=value2[;property], ...
//
// Values may be percent-encoded. Properties (anything after the first ';' in
// a member) are ignored.
func parseBundleRefFromBaggage(header string) BundleRef {
	if header == "" {
		return BundleRef{}
	}

	var ref BundleRef
	for _, member := range strings.Split(header, ",") {
		member = strings.TrimSpace(member)
		if member == "" {
			continue
		}
		// Strip the optional property segment.
		if i := strings.Index(member, ";"); i >= 0 {
			member = member[:i]
		}
		eq := strings.Index(member, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(member[:eq])
		value := strings.TrimSpace(member[eq+1:])
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}

		switch key {
		case baggageBundleARNKey:
			ref.BundleARN = value
		case baggageBundleVersionKey:
			ref.VersionID = value
		}
	}
	return ref
}

// experimentInfo holds A/B test experiment metadata extracted from the
// W3C baggage header. The AgentCore Gateway injects these when an A/B
// test is active.
type experimentInfo struct {
	ExperimentARN string
	VariantName   string
}

// IsZero reports whether no experiment metadata was found in the baggage.
func (e experimentInfo) IsZero() bool {
	return e.ExperimentARN == "" && e.VariantName == ""
}

// parseBaggageAll extracts both the bundle reference and experiment info
// from a W3C baggage header in a single pass.
func parseBaggageAll(header string) (BundleRef, experimentInfo) {
	if header == "" {
		return BundleRef{}, experimentInfo{}
	}

	var ref BundleRef
	var exp experimentInfo

	for _, member := range strings.Split(header, ",") {
		member = strings.TrimSpace(member)
		if member == "" {
			continue
		}
		if i := strings.Index(member, ";"); i >= 0 {
			member = member[:i]
		}
		eq := strings.Index(member, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(member[:eq])
		value := strings.TrimSpace(member[eq+1:])
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}

		switch key {
		case baggageBundleARNKey:
			ref.BundleARN = value
		case baggageBundleVersionKey:
			ref.VersionID = value
		case baggageExperimentARNKey:
			exp.ExperimentARN = value
		case baggageVariantNameKey:
			exp.VariantName = value
		}
	}
	return ref, exp
}
