#!/bin/bash
# Run integration tests against all Bedrock models in parallel + direct Anthropic.

REGION="eu-central-1"
RESULTS_DIR="tmp/model-results"
rm -rf "$RESULTS_DIR"
mkdir -p "$RESULTS_DIR"

# On-demand models (in-region)
ON_DEMAND_MODELS=(
  "qwen.qwen3-235b-a22b-2507-v1:0"
  "qwen.qwen3-coder-30b-a3b-v1:0"
  "qwen.qwen3-32b-v1:0"
  "minimax.minimax-m2.5"
  "minimax.minimax-m2.1"
  "openai.gpt-oss-120b-1:0"
  "openai.gpt-oss-20b-1:0"
  "nvidia.nemotron-super-3-120b"
  "zai.glm-4.7-flash"
)

# Inference profile models (cross-region, eu. prefix)
INFERENCE_MODELS=(
  "eu.anthropic.claude-opus-4-6-v1"
  "eu.anthropic.claude-sonnet-4-6"
  "eu.anthropic.claude-opus-4-5-20251101-v1:0"
  "eu.anthropic.claude-sonnet-4-5-20250929-v1:0"
  "eu.anthropic.claude-sonnet-4-20250514-v1:0"
  "eu.anthropic.claude-haiku-4-5-20251001-v1:0"
  "eu.amazon.nova-pro-v1:0"
  "eu.amazon.nova-2-lite-v1:0"
  "eu.amazon.nova-lite-v1:0"
  "eu.amazon.nova-micro-v1:0"
)

# Direct Anthropic models
ANTHROPIC_MODELS=(
  "claude-opus-4-20250514"
  "claude-sonnet-4-20250514"
  "claude-haiku-4-5-20251001"
)

# Direct OpenAI models
OPENAI_MODELS=(
  "gpt-4o"
  "gpt-4o-mini"
  "gpt-4.1"
  "gpt-4.1-mini"
  "gpt-4.1-nano"
  "gpt-5"
  "gpt-5-mini"
  "gpt-5-nano"
  "o3"
  "o3-mini"
  "o4-mini"
)

run_bedrock() {
  local model="$1"
  local safe_name=$(echo "bedrock_${model}" | tr ':.' '_')
  local logfile="$RESULTS_DIR/${safe_name}.log"

  echo "[START] bedrock/$model"
  PROVIDER=bedrock BEDROCK_MODEL="$model" AWS_REGION="$REGION" \
    go test -tags=integration -v -timeout=180s -run "TestIntegration_" ./agent/... 2>&1 > "$logfile"

  local passed=$(grep -c "^--- PASS:" "$logfile")
  local failed=$(grep -c "^--- FAIL:" "$logfile")
  local total=$((passed + failed))

  if [ $failed -eq 0 ]; then
    echo "[PASS] bedrock/$model ($passed/$total)"
  else
    echo "[FAIL] bedrock/$model ($passed/$total passed, $failed failed)"
    grep "^--- FAIL:" "$logfile" | sed 's/--- FAIL: /  ✗ /'
  fi
}

run_anthropic() {
  local model="$1"
  local safe_name=$(echo "anthropic_${model}" | tr ':.' '_')
  local logfile="$RESULTS_DIR/${safe_name}.log"

  echo "[START] anthropic/$model"
  PROVIDER=anthropic ANTHROPIC_MODEL="$model" \
    go test -tags=integration -v -timeout=180s -run "TestIntegration_" ./agent/... 2>&1 > "$logfile"

  local passed=$(grep -c "^--- PASS:" "$logfile")
  local failed=$(grep -c "^--- FAIL:" "$logfile")
  local total=$((passed + failed))

  if [ $failed -eq 0 ]; then
    echo "[PASS] anthropic/$model ($passed/$total)"
  else
    echo "[FAIL] anthropic/$model ($passed/$total passed, $failed failed)"
    grep "^--- FAIL:" "$logfile" | sed 's/--- FAIL: /  ✗ /'
  fi
}

run_openai() {
  local model="$1"
  local safe_name=$(echo "openai_${model}" | tr ':.' '_')
  local logfile="$RESULTS_DIR/${safe_name}.log"

  echo "[START] openai/$model"
  PROVIDER=openai OPENAI_MODEL="$model" \
    go test -tags=integration -v -timeout=180s -run "TestIntegration_" ./agent/... 2>&1 > "$logfile"

  local passed=$(grep -c "^--- PASS:" "$logfile")
  local failed=$(grep -c "^--- FAIL:" "$logfile")
  local total=$((passed + failed))

  if [ $failed -eq 0 ]; then
    echo "[PASS] openai/$model ($passed/$total)"
  else
    echo "[FAIL] openai/$model ($passed/$total passed, $failed failed)"
    grep "^--- FAIL:" "$logfile" | sed 's/--- FAIL: /  ✗ /'
  fi
}

echo "============================================"
echo "Running integration tests for all models"
echo "Bedrock region: $REGION"
echo "============================================"
echo ""

pids=()

for model in "${ON_DEMAND_MODELS[@]}"; do
  run_bedrock "$model" &
  pids+=($!)
done

for model in "${INFERENCE_MODELS[@]}"; do
  run_bedrock "$model" &
  pids+=($!)
done

for model in "${ANTHROPIC_MODELS[@]}"; do
  run_anthropic "$model" &
  pids+=($!)
done

for model in "${OPENAI_MODELS[@]}"; do
  run_openai "$model" &
  pids+=($!)
done

for pid in "${pids[@]}"; do
  wait "$pid"
done

echo ""
echo "============================================"
echo "All tests complete. Logs in $RESULTS_DIR/"
echo "============================================"
