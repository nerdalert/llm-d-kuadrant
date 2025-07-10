## WIP - AuthPolicy Callback (not-working)

### Build

```shell
docker build -f Containerfile -t ghcr.io/nerdalert/usage-tracking:latest .
docker push ghcr.io/nerdalert/usage-tracking:latest
```

### Run

```shell
kubectl apply -f usage-tracking.yaml
kubectl apply -f usage-tracking.yaml
```

### Debug

```shell
kubectl get crd authpolicies.kuadrant.io -o yaml | grep -A 10 -B 10 callback
kubectl -n kuadrant-system logs -f deployment/authorino

# Empty 😭
curl -sG --data-urlencode 'query=llm_requests_total'   http://localhost:9090/api/v1/query | jq '.data.result'
[]

# No logs user-tracking logs 😭
kubectl -n llm-d logs deploy/usage-tracking --tail=20
{"time":"2025-07-10T05:00:38.397161815Z","level":"INFO","msg":"pprof endpoints enabled at /debug/pprof"}
{"time":"2025-07-10T05:00:38.397341249Z","level":"INFO","msg":"usage-tracking listening","addr":":8080","level":"DEBUG"}


# Auth still works, just no callbacks:
curl -s -o /dev/null -w '%{http_code}\n'   -X POST http://localhost:8000/v1/completions   -H 'Authorization:APIKEY premiumuser1_key'   -H 'Content-Type: application/json'   -d '{"model":"Qwen/Qwen3-0.6B","prompt":"ping"}'
200

# Authorino log for the request
{"level":"info","ts":"2025-07-10T05:06:39Z","logger":"authorino.service.auth","msg":"incoming authorization request","request id":"e738d0d2-5650-4931-9176-63cc62d8b20d","object":{"source":{"address":{"Address":{"SocketAddress":{"address":"127.0.0.1:59228","PortSpecifier":{"PortValue":59228}}}}},"destination":{"address":{"Address":{"SocketAddress":{"address":"127.0.0.1:80","PortSpecifier":{"PortValue":80}}}}},"request":{"http":{"id":"e738d0d2-5650-4931-9176-63cc62d8b20d","method":"POST","path":"/v1/completions","host":"localhost:8000","scheme":"http"}}}}
{"level":"info","ts":"2025-07-10T05:06:39Z","logger":"authorino.service.auth","msg":"outgoing authorization response","request id":"e738d0d2-5650-4931-9176-63cc62d8b20d","authorized":true,"response":"OK"}
```
