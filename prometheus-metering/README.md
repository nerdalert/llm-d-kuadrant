
## Cost metrics add-on (Limitador → Prometheus)

- Pre-req: Initial llm-d/kuadrant setup in [README.md](../README.md)
- Prometheus isn't the right place for the costing. It is the right place to capture usage.
- Hardcoded time windows dont belong here either, e.g. (24hr, 5m etc). Adding the rules to express the need for an application scraping Prometheus to manage billing totals and presentation.

### What the rules do

* **`llmd_authorized_call_price_usd`** – a constant scalar that represents the unit price (USD) of a *successful* request.
  `vector(0.002)` means **0.2 ¢** per call. Change the cost if to raise or lower pricing.

* **`llmd_authorized_calls_24h_total`** – a rolling, 24-hour *volume* counter.
  PromQL: `increase(authorized_calls{namespace="kuadrant-system"}[24h])`

* **`llmd_request_cost_usd_24h_total`** – rolling, 24-hour *spend* = `volume × price`.

* **`llmd_authorized_calls_total`** - raw authorized totals

### Apply the manifests

```bash
kubectl apply -f llm-d-monitoring/limitador-servicemonitor.yaml
kubectl apply -f llm-d-monitoring/limitador-costing-prometheus-rules.yaml
```

<details>
<summary>llm-d-monitoring/limitador-servicemonitor.yaml</summary>

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: limitador
  namespace: llm-d-monitoring      # same ns as Prometheus
  labels:
    release: prometheus
    app.kubernetes.io/name: limitador
    app.kubernetes.io/component: limitador
spec:
  namespaceSelector:
    matchNames:
      - kuadrant-system            # scrape the Service in that ns
  selector:
    matchLabels:
      app: limitador               # matches Service’s only label
  endpoints:
    - port: http                   # port 8080 in the Service
      path: /metrics
      scheme: http
      interval: 30s
      scrapeTimeout: 10s
```

</details>

<details>
<summary>llm-d-monitoring/limitador-costing-prometheus-rules.yaml</summary>

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: limitador-costing
  namespace: llm-d-monitoring
  labels:
    release: prometheus
    app.kubernetes.io/name: llmd
    app.kubernetes.io/component: cost-metrics
spec:
  groups:
    - name: llm-costing
      interval: 30s
      rules:
        # ──────────────────────────────────────────────
        # Constant price per *authorized* call (USD)
        # ──────────────────────────────────────────────
        - record: llmd_authorized_call_price_usd
          expr: vector(0.002)         # <— adjust if you change your price

        # ──────────────────────────────────────────────
        # Rolling 24h total *authorized* calls
        # ──────────────────────────────────────────────
        - record: llmd_authorized_calls_24h_total
          expr: |
            increase(
              authorized_calls{namespace="kuadrant-system"}[24h]
            )

        - record: llmd_request_cost_usd_24h_total
          expr: |
            llmd_authorized_calls_24h_total
              * on() group_left(plan)
                llmd_authorized_call_price_usd

        # ──────────────────────────────────────────────
        # Raw *authorized* calls counter
        # ──────────────────────────────────────────────
        - record: llmd_authorized_calls_total
          expr: authorized_calls{namespace="kuadrant-system"}

        # ──────────────────────────────────────────────
        # Raw *limited* calls counter
        # ──────────────────────────────────────────────
        - record: llmd_authorized_calls_total
          expr: limited_calls{namespace="kuadrant-system"}
```
</details>

- Forward the Prometheus service. Also ensure the gateway service is still forwarding from the initial setup in [README](../README.md)

```shell
kubectl -n llm-d-monitoring port-forward svc/prometheus-kube-prometheus-prometheus 9090:9090 &
# Gateway → localhost:8000
kubectl -n llm-d port-forward svc/llm-d-inference-gateway-istio 8000:80 &
```

### Validation

```bash
# FREE burst (expects many 429s after the first two)
for i in {1..15}; do
  printf "free req #%-2s -> " "$i"
  curl -s -o /dev/null -w "%{http_code}\n" \
       -X POST http://localhost:8000/v1/completions \
       -H 'Authorization:APIKEY freeuser1_key' \
       -H 'Content-Type: application/json' \
       -d '{"model":"Qwen/Qwen3-0.6B","prompt":"Cats or Dogs?"}'
done

# PREMIUM burst
for i in {1..15}; do
  printf "premium req #%-2s -> " "$i"
  curl -s -o /dev/null -w "%{http_code}\n" \
       -X POST http://localhost:8000/v1/completions \
       -H 'Authorization:APIKEY premiumuser1_key' \
       -H 'Content-Type: application/json' \
       -d '{"model":"Qwen/Qwen3-0.6B","prompt":"Cats or Dogs?"}'
done
```


- Query the recording rules in Prometheus

```bash
# Price vector – returns 0.002
curl -sG --data-urlencode 'query=llmd_authorized_call_price_usd' \
     http://localhost:9090/api/v1/query | jq '.data.result'

# 24-hour authorised-call total 24hr window
curl -sG --data-urlencode 'query=llmd_authorized_calls_24h_total' \
     http://localhost:9090/api/v1/query | jq '.data.result'

# 24-hour cost
curl -sG --data-urlencode 'query=llmd_request_cost_usd_24h_total' \
     http://localhost:9090/api/v1/query | jq '.data.result'

# ---------------------------------------------
# Dynamic time windows:
# ---------------------------------------------
# 1) Choose your window, e.g. "6h", "12h", "24h", "7d"
TIME_FRAME="24h"

# 2) Get the total calls in that window
curl -sG \
  --data-urlencode "query=sum(increase(authorized_calls{namespace=\"kuadrant-system\"}[${TIME_FRAME}]))" \
  http://localhost:9090/api/v1/query | jq '.data.result'

# 3) Get and calculate the total cost at $0.002 per call
curl -sG \
  --data-urlencode "query=sum(increase(authorized_calls{namespace=\"kuadrant-system\"}[${TIME_FRAME}]) * 0.002)" \
  http://localhost:9090/api/v1/query | jq '.data.result'
  
# 4) Get the total authorized calls in that window
curl -sG \
  --data-urlencode "query=sum(increase(authorized_calls{namespace=\"kuadrant-system\"}[${TIME_FRAME}]))" \
  http://localhost:9090/api/v1/query | jq '.data.result'

# 5) Get the total rate-limited calls in that window
curl -sG \
  --data-urlencode "query=sum(increase(limited_calls{namespace=\"kuadrant-system\"}[${TIME_FRAME}]))" \
  http://localhost:9090/api/v1/query | jq '.data.result'
```

*Tip – numbers only*:
append `| jq -r '.[0].value[1]'` to strip the JSON wrapper.


- Example Output

```bash
# ---------------------------------------------
# Hourly Rate – returns 0.002
# ---------------------------------------------
curl -sG --data-urlencode 'query=llmd_authorized_call_price_usd' \
     http://localhost:9090/api/v1/query | jq '.data.result'
[
  {
    "metric": {
      "__name__": "llmd_authorized_call_price_usd"
    },
    "value": [
      1752026397.535,
      "0.002"
    ]
  }
]

# ---------------------------------------------
# 24-hour authorised-call count total
# ---------------------------------------------
 curl -sG --data-urlencode 'query=llmd_authorized_calls_24h_total' \
     http://localhost:9090/api/v1/query | jq '.data.result'
[
  {
    "metric": {
      "__name__": "llmd_authorized_calls_24h_total",
      "container": "limitador",
      "endpoint": "http",
      "instance": "10.244.0.20:8080",
      "job": "limitador-limitador",
      "limitador_namespace": "llm-d/qwen-qwen3-0-6b",
      "namespace": "kuadrant-system",
      "pod": "limitador-limitador-84bdfb4747-x72lc",
      "service": "limitador-limitador"
    },
    "value": [
      1752026449.101,
      "107.08006728045294"
    ]
  }
]

# ---------------------------------------------
# 24-hour cost volume × price = $0.21
# ---------------------------------------------
curl -sG --data-urlencode 'query=llmd_request_cost_usd_24h_total' \
     http://localhost:9090/api/v1/query | jq '.data.result'
[
  {
    "metric": {
      "__name__": "llmd_request_cost_usd_24h_total",
      "container": "limitador",
      "endpoint": "http",
      "instance": "10.244.0.20:8080",
      "job": "limitador-limitador",
      "limitador_namespace": "llm-d/qwen-qwen3-0-6b",
      "namespace": "kuadrant-system",
      "pod": "limitador-limitador-84bdfb4747-x72lc",
      "service": "limitador-limitador"
    },
    "value": [
      1752026554.193,
      "0.2141593808391916"
    ]
  }
]

# ---------------------------------------------
# Dynamic time 24-hour authorised-call count total
# ---------------------------------------------
$ TIME_FRAME="24h"
$ curl -sG   --data-urlencode "query=sum(increase(authorized_calls{namespace=\"kuadrant-system\"}[${TIME_FRAME}]))"   http://localhost:9090/api/v1/query | jq '.data.result'
[
  {
    "metric": {},
    "value": [
      1752070259.516,
      "107.03413658068732"
    ]
  }
]

# ---------------------------------------------
# Dynamic time frame (24-hour) cost volume × price = $0.21
# ---------------------------------------------
$ TIME_FRAME="24h"
curl -sG \
  --data-urlencode "query=sum(increase(authorized_calls{namespace=\"kuadrant-system\"}[${TIME_FRAME}]) * 0.002)" \
  http://localhost:9090/api/v1/query | jq '.data.result'
[
  {
    "metric": {},
    "value": [
      1752070346.015,
      "0.214057387359113"
    ]
  }
]

# ---------------------------------------------
# Dynamic time 24-hour rate-limited call count total
# ---------------------------------------------
curl -sG \
  --data-urlencode "query=sum(increase(limited_calls{namespace=\"kuadrant-system\"}[${TIME_FRAME}]))" \
  http://localhost:9090/api/v1/query | jq '.data.result'
[
  {
    "metric": {},
    "value": [
      1752071103.040,
      "287.104884329798"
    ]
  }
]
```

### Troubleshooting

```bash
# Admission-webhook rejections (syntax errors in rules)
kubectl -n llm-d-monitoring logs deploy/prometheus-kube-prometheus-operator | grep rule

# List all PrometheusRule objects
kubectl get prometheusrule -A

# Inspect the costing rule
kubectl get prometheusrule limitador-costing -n llm-d-monitoring -o yaml
```

### One-liner recap

1. Run the **llm-d quick-start** and **Kuadrant** steps from the main [README](../README.md).
2. `kubectl apply` the two YAML files above.
3. Fire the validation loops and watch `llmd_request_cost_usd_24h_total` climb.

You now have real-time cost visibility for every authorised call served by your LLM Inference Gateway.
