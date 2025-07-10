# WIP - AuthPolicy Callback

## Summary

* **API-key identity**

    * Created Kubernetes `Secret`s for each user, annotated with their tier/group (`free` / `premium`) and user-id.

* **Gateway protection (AuthPolicy)**

    * Authenticates incoming requests by API key.
    * Authorizes only “free” or “premium” groups via an inline OPA rule.
    * Exposes `userid` and `groups` as dynamic metadata.
    * Adds a **callback** that, on successful auth, POSTs those fields to an accounting endpoint.

* **Lightweight accounting service**

    * Smoll Go server (`usage-tracking`)

        * `POST /track` → increments Prometheus counter `llm_requests_total{user,groups,path}`.
        * Exposes `/metrics`, `/healthz`, optional `pprof`.

* **K8s runtime objects**

    * Deployment + Service for `usage-tracking` (named port `http`).
    * ServiceMonitor (Prometheus Operator) to scrape `/metrics` every 15s.

* **Data flow**

    1. Client hits Gateway with `Authorization: APIKEY …`.
    2. Authorino authenticates, authorizes, then fires the callback to the new usage-tracking service.
    3. `usage-tracking` records the request.
    4. Prometheus scrapes the usage-tracking counter; queries like `llm_requests_total` now return per-user usage records.

Result: every authorised LLM request is counted in Prometheus with user, group, and path labels—ready for dashboards or billing.

### Build

```shell
docker build -f Containerfile -t ghcr.io/nerdalert/usage-tracking:latest .
docker push ghcr.io/nerdalert/usage-tracking:latest
kubectl -n llm-d set image deploy/usage-tracking usage-tracking=ghcr.io/nerdalert/usage-tracking:latest
kubectl -n llm-d rollout restart deploy/usage-tracking
```

### Run

```shell
kubectl apply -f authpolicy-callback.yaml
kubectl apply -f usage-tracking.yaml
```

### Watch Logs

Make some calls with a couple of keys.

```shell
```bash
# FREE burst
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

See the callback. That calls the user-tracking go service that will serve as an endpoint for Prometheus to scrape. Callbacks are async so * should * be negligible on performance.

```shell
# kubectl -n kuadrant-system logs -f deployment/authorino
2025-07-10T06:01:03Z	DEBUG	authorino.service.auth.authpipeline.callback.http	sending request	{"request id": "c7f6e617-6800-4c0d-b463-f2f78850cf7b", "method": "POST", "url": "http://usage-tracking.llm-d.svc.cluster.local/track", "headers": {"Content-Type":["application/json"]}, "body": "{\"groups\":\"premium\",\"host\":\"localhost:8000\",\"method\":\"POST\",\"path\":\"/v1/completions\",\"user\":\"premiumuser1\"}"}
2025-07-10T06:01:03Z	DEBUG	authorino.service.auth.authpipeline.callbacks	callback executed	{"request id": "c7f6e617-6800-4c0d-b463-f2f78850cf7b", "config": {"Name":"usage-accounting","Priority":0,"Conditions":{"Left":null,"Right":null},"Metrics":false,"HTTP":{"Endpoint":"http://usage-tracking.llm-d.svc.cluster.local/track","DynamicEndpoint":null,"Method":"POST","Body":{},"Parameters":[],"Headers":[],"ContentType":"application/json","SharedSecret":"","OAuth2":null,"OAuth2TokenForceFetch":false,"AuthCredentials":null}}, "object": ""}
2025-07-10T06:01:03Z	INFO	authorino.service.auth	outgoing authorization response	{"request id": "c7f6e617-6800-4c0d-b463-f2f78850cf7b", "authorized": true, "response": "OK"}
2025-07-10T06:01:03Z	DEBUG	authorino.service.auth	outgoing authorization response	{"request id": "c7f6e617-6800-4c0d-b463-f2f78850cf7b", "authorized": true, "response": "OK"}
```

### View per user usage in Prometheus

Now that Prometheus has scraped the user-tracking service (callback target), you can now scrape the user/group/path->request call counts.

```shell
$ curl -sG --data-urlencode 'query=llm_requests_total'   http://localhost:9090/api/v1/query | jq '.data.result'
[
  {
    "metric": {
      "__name__": "llm_requests_total",
      "container": "usage-tracking",
      "endpoint": "http",
      "groups": "premium",
      "instance": "10.244.3.120:8080",
      "job": "usage-tracking",
      "namespace": "llm-d",
      "path": "/v1/completions",
      "pod": "usage-tracking-657d7795dd-7pb8p",
      "service": "usage-tracking",
      "user": "premiumuser1"
    },
    "value": [
      1752128602.635,
      "5"
    ]
  },
  {
    "metric": {
      "__name__": "llm_requests_total",
      "container": "usage-tracking",
      "endpoint": "http",
      "groups": "free",
      "instance": "10.244.3.120:8080",
      "job": "usage-tracking",
      "namespace": "llm-d",
      "path": "/v1/completions",
      "pod": "usage-tracking-657d7795dd-7pb8p",
      "service": "usage-tracking",
      "user": "freeuser1"
    },
    "value": [
      1752128602.635,
      "45"
    ]
  }
]
```

### Debug

```shell
kubectl get crd authpolicies.kuadrant.io -o yaml | grep -A 10 -B 10 callback
kubectl -n kuadrant-system logs -f deployment/authorino
```

Curl the user-tracking service directly for prometheus logs.

```shell
# ⬇︎ forward container port 8080 to local port 18080
kubectl -n llm-d port-forward deploy/usage-tracking 18080:8080 &

# now query locally
curl -s http://localhost:18080/metrics | grep llm_requests_total
Handling connection for 18080
# HELP llm_requests_total Successful LLM requests labelled by user, group and path.
# TYPE llm_requests_total counter
llm_requests_total{groups="premium",path="/v1/completions",user="premiumuser1"} 5

```

- Setup Authorino debugging

```shell
# Add debugging
apiVersion: operator.authorino.kuadrant.io/v1beta1
kind: Authorino
metadata:
  name: authorino
  namespace: kuadrant-system
spec:
  logLevel: debug
  logMode: development
  supersedingHostSubsets: true

kubectl apply -f authorino-debug.yaml
Warning: resource authorinos/authorino is missing the kubectl.kubernetes.io/last-applied-configuration annotation which is required by kubectl apply. kubectl apply should only be used on resources created declaratively by either kubectl create --save-config or kubectl apply. The missing annotation will be patched automatically.
authorino.operator.authorino.kuadrant.io/authorino configured
kubectl -n kuadrant-system rollout restart deploy authorino
deployment.apps/authorino restarted
```
