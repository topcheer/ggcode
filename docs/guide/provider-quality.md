# Provider A/B Quality Comparison

ggcode automatically scores each agent run on quality signals to enable
data-driven provider/model comparison. Unlike failover (which reacts to
failures), quality scoring proactively measures how well a model performs
so you can make informed switching decisions.

## How It Works

After each run completes, the **Response Quality Scorer** computes a 0.0-1.0
score from five weighted signals derived from existing run statistics --
no extra API calls or LLM overhead:

| Signal | Weight | Description |
|--------|--------|-------------|
| Success | 40% | Whether the run completed without error |
| Tool Efficiency | 25% | Ratio of successful tool calls to total |
| Error Rate | 15% | Errors per iteration (fewer is better) |
| Iteration Efficiency | 10% | Iterations used vs. expected baseline (3) |
| Context Efficiency | 10% | How much of the context window was consumed |

## Accessing Comparison Data

The scorer is built into the Agent struct and automatically records every
run. Use these accessors:

```go
// Get structured comparison data
comps := agent.QualityComparison()
for _, c := range comps {
    fmt.Printf("%s/%s: avg=%.3f success=%.1f%% runs=%d\n",
        c.Provider, c.Model, c.AvgScore, c.SuccessRate*100, c.RunCount)
}

// Get a human-readable report
fmt.Println(agent.QualityReport())
```

## Use Cases

- **Model switching decisions**: Compare average scores across models to
  pick the best performer for your workload.
- **Cost vs. quality tradeoffs**: Correlate quality scores with cost data
  from the `cost` package to find the best value model.
- **Regression detection**: If a model's score drops over time, it may
  indicate API changes or degraded performance.

## Implementation

- File: `internal/agent/response_quality.go`
- Tests: `internal/agent/response_quality_test.go`
- Wired into: `internal/agent/reflection.go` (maybeReflect)
- Capacity: 100 most recent runs (ring buffer)
- Thread-safe via `sync.RWMutex`
