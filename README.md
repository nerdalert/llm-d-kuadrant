

# Kuadrant-Backed Quotas for the **LLM-d Inference Gateway**

This guide shows how to secure an LLM-d Istio Gateway with **API-key authentication** and **per-tier rate limits** (free vs premium) demo using Kuadrant (Authorino + Limitador).

See for [prometheus-metering](prometheus-metering/README.md) scraping metrics via Prometheus
---

## 1 · Prerequisites

| Requirement | Version                                                                          |
|-------------|----------------------------------------------------------------------------------|
| Kubernetes  | 1.23+                                                                            |
| Helm        | 3.9+                                                                             |
| Gateway API | `gateway.networking.k8s.io/v1beta1` CRDs present (deployed with llm-d)           |
| Istio       | Sidecar injection enabled on the Gateway namespace (deployed with llm-d)         |
| llm-d       | [llm-d quickstart](https://github.com/llm-d/llm-d-deployer/tree/main/quickstart) |


---

## 2 · Install Kuadrant

```bash
helm repo add kuadrant https://kuadrant.io/helm-charts
helm repo update

helm install kuadrant-operator kuadrant/kuadrant-operator \
  --create-namespace \
  --namespace kuadrant-system
````

Deploy the control plane:

```bash
cat <<EOF | kubectl apply -f -
apiVersion: kuadrant.io/v1beta1
kind: Kuadrant
metadata:
  name: kuadrant
  namespace: kuadrant-system
EOF
```

Confirm readiness:

```bash
kubectl get kuadrant kuadrant -n kuadrant-system \
  -o=jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
```

---

## 3 · Create API-Key Secrets, AuthPolicy, and RateLimitPolicy

Manifest [kuadrant-rate-limit.yaml](kuadrant-rate-limit.yaml)

<details>
<summary>Complete manifest (<code>kuadrant-rate-limit.yaml</code>)</summary>

```yaml
###############################################################################
# 1.  API-KEY SECRETS – annotated with “free” or “premium”
###############################################################################
apiVersion: v1
kind: Secret
metadata:
  name: premiumuser1-apikey
  namespace: llm-d
  labels:
    kuadrant.io/auth-secret: "true"
    app: my-llm
  annotations:
    kuadrant.io/groups: premium
    secret.kuadrant.io/user-id: premiumuser1
stringData:
  api_key: premiumuser1_key
type: Opaque
---
apiVersion: v1
kind: Secret
metadata:
  name: free1-apikey
  namespace: llm-d
  labels:
    kuadrant.io/auth-secret: "true"
    app: my-llm
  annotations:
    kuadrant.io/groups: free
    secret.kuadrant.io/user-id: freeuser1
stringData:
  api_key: freeuser1_key
type: Opaque
---
apiVersion: v1
kind: Secret
metadata:
  name: free2-apikey
  namespace: llm-d
  labels:
    kuadrant.io/auth-secret: "true"
    app: my-llm
  annotations:
    kuadrant.io/groups: free
    secret.kuadrant.io/user-id: freeuser2
stringData:
  api_key: freeuser2_key
type: Opaque

###############################################################################
# 2.  AUTHPOLICY – API-key auth on the Gateway
###############################################################################
---
apiVersion: kuadrant.io/v1
kind: AuthPolicy
metadata:
  name: llm-api-keys
  namespace: llm-d
spec:
  targetRef:
    group: gateway.networking.k8s.io
    kind: Gateway
    name: llm-d-inference-gateway
  rules:
    authentication:
      api-key-users:
        apiKey:
          allNamespaces: true
          selector:
            matchLabels:
              app: my-llm
        credentials:
          authorizationHeader:
            prefix: APIKEY
    response:
      success:
        filters:
          identity:
            json:
              properties:
                userid:
                  selector: auth.identity.metadata.annotations.secret\.kuadrant\.io/user-id
                groups:
                  selector: auth.identity.metadata.annotations.kuadrant\.io/groups
    authorization:
      allow-groups:
        opa:
          rego: |
            groups := split(object.get(input.auth.identity.metadata.annotations, "kuadrant.io/groups", ""), ",")
            allow { groups[_] == "free" }
            allow { groups[_] == "premium" }

###############################################################################
# 3.  RATELIMITPOLICY – 2 req / 2 min (free) · 10 req / 2 min (premium)
###############################################################################
---
apiVersion: kuadrant.io/v1
kind: RateLimitPolicy
metadata:
  name: basic-rate-limits
  namespace: llm-d
spec:
  targetRef:
    group: gateway.networking.k8s.io
    kind: Gateway
    name: llm-d-inference-gateway
  limits:
    free-user-requests:
      rates:
        - limit: 2
          window: 2m
      when:
        - predicate: |
            auth.identity.groups.split(",").exists(g, g == "free")
      counters:
        - expression: auth.identity.userid
    premium-user-requests:
      rates:
        - limit: 10
          window: 2m
      when:
        - predicate: |
            auth.identity.groups.split(",").exists(g, g == "premium")
      counters:
        - expression: auth.identity.userid
```

</details>

Apply it:

```bash
kubectl apply -f kuadrant-rate-limit.yaml
```

---

## 4 · Port-forward Endpoints (dev clusters)

```bash
# Gateway → localhost:8000
kubectl -n llm-d port-forward svc/llm-d-inference-gateway-istio 8000:80 &

# Limitador admin / metrics → localhost:8080
kubectl -n kuadrant-system port-forward svc/limitador-limitador 8080:8080 &
```

---

## 5 · Smoke-tests

### 5.1 Free-tier burst (expect 2 × 200, then 429)

```bash
for i in {1..15}; do
  printf "free req #%-2s -> " "$i"
  curl -s -o /dev/null -w "%{http_code}\n" \
       -X POST http://localhost:8000/v1/completions \
       -H 'Authorization:APIKEY freeuser1_key' \
       -H 'Content-Type: application/json' \
       -d '{"model":"Qwen/Qwen3-0.6B","prompt":"Cats or Dogs?"}'
done
```

Example output:

```shell
free req #1  -> 200
free req #2  -> 200
free req #3  -> 429
free req #4  -> 429
free req #5  -> 429
free req #6  -> 429
free req #7  -> 429
free req #8  -> 429
free req #9  -> 429
free req #10 -> 429
free req #11 -> 429
free req #12 -> 429
free req #13 -> 429
free req #14 -> 429
free req #15 -> 429
```

Manual single curl:

```bash
curl -X POST http://localhost:8000/v1/completions \
     -H 'Authorization:APIKEY freeuser1_key' \
     -H 'Content-Type: application/json' \
     -d '{"model":"Qwen/Qwen3-0.6B","prompt":"Cats or Dogs?"}'
```

### 5.2 Premium-tier burst (expect 10 × 200, then 429)

```bash
for i in {1..15}; do
  printf "premium req #%-2s -> " "$i"
  curl -s -o /dev/null -w "%{http_code}\n" \
       -X POST http://localhost:8000/v1/completions \
       -H 'Authorization:APIKEY premiumuser1_key' \
       -H 'Content-Type: application/json' \
       -d '{"model":"Qwen/Qwen3-0.6B","prompt":"Cats or Dogs?"}'
done
```

Output:

```shell
premium req #1  -> 200
premium req #2  -> 200
premium req #3  -> 200
premium req #4  -> 200
premium req #5  -> 200
premium req #6  -> 200
premium req #7  -> 200
premium req #8  -> 200
premium req #9  -> 200
premium req #10 -> 200
premium req #11 -> 429
premium req #12 -> 429
premium req #13 -> 429
premium req #14 -> 429
premium req #15 -> 429
```

---

## 6 · Inspect Limitador

### 6.1 Active limits

```bash
curl http://localhost:8080/limits/llm-d%2Fqwen-qwen3-0-6b | jq
```

<details>
<summary>Output</summary>

```json
[
  {
    "id": null,
    "namespace": "llm-d/qwen-qwen3-0-6b",
    "max_value": 10,
    "seconds": 120,
    "name": null,
    "conditions": [
      "descriptors[0][\"limit.premium_user_requests__4f559388\"] == \"1\""
    ],
    "variables": [
      "descriptors[0][\"auth.identity.userid\"]"
    ]
  },
  {
    "id": null,
    "namespace": "llm-d/qwen-qwen3-0-6b",
    "max_value": 2,
    "seconds": 120,
    "name": null,
    "conditions": [
      "descriptors[0][\"limit.free_user_requests__3a36ecc2\"] == \"1\""
    ],
    "variables": [
      "descriptors[0][\"auth.identity.userid\"]"
    ]
  }
]
```

</details>


### 6.2 Metrics (Prometheus format)

```bash
curl http://localhost:8080/metrics | grep -E 'authorized_calls|limited_calls'
```

Sample:

```
# HELP limited_calls Limited calls
limited_calls{limitador_namespace="llm-d/qwen-qwen3-0-6b"} 17
# HELP authorized_calls Authorized calls
authorized_calls{limitador_namespace="llm-d/qwen-qwen3-0-6b"} 38
```

---

## 7 · Troubleshooting

```bash
# Tail Authorino
kubectl -n kuadrant-system logs -f deployment/authorino

# Debug Envoy in the Gateway pod
export ISTIO_POD=$(kubectl -n llm-d get pods -l app=llm-d-inference-gateway-istio -o jsonpath='{.items[0].metadata.name}')
istioctl -n llm-d proxy-config log $ISTIO_POD --level debug
kubectl -n llm-d logs -f $ISTIO_POD -c istio-proxy
```

---

## 8 · Clean-up

```bash
kubectl delete -f kuadrant-rate-limit.yaml
helm uninstall kuadrant-operator -n kuadrant-system
```

---

### Summary

* **Secrets** tag users as *free* or *premium*.
* **AuthPolicy** authenticates **APIKEY \<key>** and exposes `userid` / `groups`.
* **RateLimitPolicy** enforces **2 req / 2 min** for free and **10 req / 2 min** for premium users.
* **Limitador** provides live JSON inspection via `/limits/*` and Prometheus metrics at `/metrics`.

Your Gateway now delivers multi-tenant LLM access—securely and predictably.

```
