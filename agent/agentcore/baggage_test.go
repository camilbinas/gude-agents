package agentcore

import "testing"

func TestParseBundleRefFromBaggage_BothKeys(t *testing.T) {
	header := "aws.agentcore.configbundle_arn=arn:aws:bedrock-agentcore:us-east-1:1:bundle/abc," +
		"aws.agentcore.configbundle_version=v1"
	got := parseBundleRefFromBaggage(header)
	if got.BundleARN != "arn:aws:bedrock-agentcore:us-east-1:1:bundle/abc" {
		t.Errorf("ARN = %q, want full ARN", got.BundleARN)
	}
	if got.VersionID != "v1" {
		t.Errorf("VersionID = %q, want v1", got.VersionID)
	}
}

func TestParseBundleRefFromBaggage_HandlesProperties(t *testing.T) {
	header := "aws.agentcore.configbundle_arn=arn:partition:svc::acct:thing;tier=a," +
		"aws.agentcore.configbundle_version=v2"
	got := parseBundleRefFromBaggage(header)
	if got.BundleARN != "arn:partition:svc::acct:thing" {
		t.Errorf("ARN = %q, did not strip property", got.BundleARN)
	}
	if got.VersionID != "v2" {
		t.Errorf("VersionID = %q, want v2", got.VersionID)
	}
}

func TestParseBundleRefFromBaggage_PercentDecoded(t *testing.T) {
	header := "aws.agentcore.configbundle_arn=arn%3Aaws%3Aexample%3A%3Abundle%2Fid," +
		"aws.agentcore.configbundle_version=v3"
	got := parseBundleRefFromBaggage(header)
	if got.BundleARN != "arn:aws:example::bundle/id" {
		t.Errorf("ARN = %q, want decoded", got.BundleARN)
	}
}

func TestParseBundleRefFromBaggage_IgnoresOtherKeys(t *testing.T) {
	header := "userId=alice,trace=abc,aws.agentcore.configbundle_arn=arn,aws.agentcore.configbundle_version=v1"
	got := parseBundleRefFromBaggage(header)
	if got.BundleARN != "arn" || got.VersionID != "v1" {
		t.Errorf("got %+v, want only AgentCore keys", got)
	}
}

func TestParseBundleRefFromBaggage_EmptyHeader(t *testing.T) {
	if got := parseBundleRefFromBaggage(""); !got.IsZero() {
		t.Errorf("empty header should yield zero ref, got %+v", got)
	}
}

func TestParseBundleRefFromBaggage_OnlyARN(t *testing.T) {
	got := parseBundleRefFromBaggage("aws.agentcore.configbundle_arn=arn")
	if got.BundleARN != "arn" {
		t.Errorf("ARN not parsed: %+v", got)
	}
	if got.VersionID != "" {
		t.Errorf("missing version should remain empty, got %q", got.VersionID)
	}
}
