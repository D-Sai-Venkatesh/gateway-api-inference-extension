# Load Testing Tool

A simple load testing tool that simulates parallel inference requests grouped by workloads, each with a `x-program-context` header.

## Building

```bash
go build -o loadtest .
```

## Usage

```bash
./loadtest -url http://localhost:8081/v1/completions -model meta-llama/Llama-3.1-8B-Instruct
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-url` | `http://localhost:8080/v1/completions` | Target URL |
| `-model` | `test-model` | Model name |
| `-num-workloads` | `5` | Number of workloads |
| `-requests-per-workload` | `20` | Requests per workload |
| `-parallel-per-workload` | `5` | Parallel requests per workload |
| `-min-words` | `5` | Min words in prompt |
| `-max-words` | `20` | Max words in prompt |
| `-max-tokens` | `100` | Max tokens for response |
| `-timeout` | `30s` | Request timeout |

### Example
```bash
kc port-forward svc/envoy 8081 -n inf-ext-e2e
```


```bash
./loadtest \
  -url http://localhost:8081/v1/completions \
  -model meta-llama/Llama-3.1-8B-Instruct \
  -num-workloads 5 \
  -requests-per-workload 20 \
  -parallel-per-workload 10
```

Each request includes an `x-program-context` header with a workload ID and a random criticality (1-5).