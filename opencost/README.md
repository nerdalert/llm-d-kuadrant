Below is a **drop-in “step 4”** for your existing *llm-d + Kuadrant + Prometheus* demo.
It installs **OpenCost**, feeds it the *llm* spend that you already record in Prometheus, and shows how to query the costs through the OpenCost API/UI.

> **Everything up to and including the Prometheus rules in your previous README is assumed to be in place and working.**
> Namespace names, service names and ports follow the objects you already have (`llm-d-monitoring` for Prometheus, `kuadrant-system` for Limitador).

---

## 4 · Add OpenCost for cluster-wide & LLM spend

### 4.1 Install OpenCost (Helm)

```bash
helm repo add opencost https://opencost.github.io/opencost-helm-chart
helm repo update

helm upgrade --install opencost opencost/opencost \
  --namespace opencost \
  --create-namespace \
  --set prometheus.enabled=false \
  --set opencost.prometheus.serverAddress=http://prometheus-kube-prometheus-prometheus.llm-d-monitoring.svc.cluster.local:9090
```

*The pod starts a service called `opencost` on port 9003.*

View logs if the pod does not come up cleanly

```shell
kubectl -n opencost logs deploy/opencost --tail=20
```

---

### 4.2 Bridge the LLM metric into OpenCost

OpenCost’s cost-engine understands **ExternalCost** CRDs.
The little CronJob below re-publishes the *roll-up spend* you’ve already built (`llmd_request_cost_usd_24h_total`) into that CRD every 30 minutes.

<details>
<summary>llm-d-monitoring/llmd-external-cost-cronjob.yaml</summary>

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: llmd-external-cost
  namespace: llm-d-monitoring
spec:
  schedule: "* * * * *"             # every 1 min
  concurrencyPolicy: Replace       # kill & replace any running job
  jobTemplate:
    spec:
      template:
        spec:
          serviceAccountName: llmd-external-cost
          restartPolicy: Never
          containers:
            - name: bridge
              image: curlimages/curl:8.8.0
              imagePullPolicy: IfNotPresent
              env:
                - name: PROM_URL
                  value: "http://prometheus-kube-prometheus-prometheus.llm-d-monitoring.svc.cluster.local:9090"
              command:
                - /bin/sh
                - -c
                - |
                  set -eu
                  COST=$(curl -sG --data-urlencode \
                          "query=llmd_request_cost_usd_24h_total" \
                          "${PROM_URL}/api/v1/query" \
                          | jq -r '.data.result[0].value[1]')
                  NOW=$(date -u +%FT%TZ)
                  YESTERDAY=$(date -u -d '24 hours ago' +%FT%TZ)

                  cat <<EOF | kubectl apply -f -
                  apiVersion: opencost.io/v1
                  kind: ExternalCost
                  metadata:
                    # still one per day; name won’t change within the same day
                    name: llmd-${NOW%%T*}
                    namespace: opencost
                  spec:
                    provider: llmd
                    window:
                      start: "${YESTERDAY}"
                      end:   "${NOW}"
                    cost: ${COST}
                    labels:
                      workload: llm-d-inference
                  EOF
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: llmd-external-cost
  namespace: llm-d-monitoring
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: llmd-external-cost
  namespace: opencost
rules:
  - apiGroups: ["opencost.io"]
    resources: ["externalcosts"]
    verbs: ["get","list","watch","create","update","patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: llmd-external-cost
  namespace: opencost
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: llmd-external-cost
subjects:
  - kind: ServiceAccount
    name: llmd-external-cost
    namespace: llm-d-monitoring
```

</details>

```bash
kubectl apply -f llm-d-monitoring/llmd-external-cost-cronjob.yaml
```

---

### Validate in OpenCost

1. **Port-forward the API/UI**

   ```bash
   kubectl -n opencost port-forward svc/opencost 9003:9003
   ```

2. **Check that ExternalCost objects are present**

   ```bash
   # raw CRDs
   kubectl -n opencost get externalcosts
   ```

3. **Query via OpenCost’s allocation API**

   ```bash
   # 24-hour window centred on “external” costs only
   curl -s "http://localhost:9003/model/external/allocation?window=24h" | jq .
   ```

   You should see a JSON object with a line similar to:

   ```json
   {
     "name": "llmd",
     "cost": 0.21,
     "start": "2025-07-08T01:00:00Z",
     "end": "2025-07-09T01:00:00Z",
     "labels": {
       "workload": "llm-d-inference"
     }
   }
   ```

4. **UI (optional)** – open [http://localhost:9003/](http://localhost:9003/) ➜ *Costs* ➜ *External* to visualise the same number alongside CPU/Memory/GPU costs. See [## Minikube web UI access - SSH Tunnel] for minikube on EC2.

---

## 5 · Validation snippets

The previous Prometheus checks still hold.
Below are the **additional** ones for OpenCost.

<details>
<summary>Curl snippets</summary>

```bash
# Current unit price
curl -sG --data-urlencode 'query=llmd_authorized_call_price_usd' \
     http://localhost:9090/api/v1/query | jq -r '.[0].value[1]'

# Rolling 24-hour authorised call volume
curl -sG --data-urlencode 'query=llmd_authorized_calls_24h_total' \
     http://localhost:9090/api/v1/query | jq -r '.[0].value[1]'

# Rolling 24-hour spend (Prometheus)
curl -sG --data-urlencode 'query=llmd_request_cost_usd_24h_total' \
     http://localhost:9090/api/v1/query | jq -r '.[0].value[1]'

# Same spend as seen by OpenCost
curl -s "http://localhost:9003/model/external/allocation?window=24h" \
     | jq -r '.data[] | select(.name=="llmd") | .cost'
```

</details>

---

## 6 · Troubleshooting

```bash
# PrometheusRule admission failures
kubectl -n llm-d-monitoring logs deploy/prometheus-kube-prometheus-operator | grep rule

# ExternalCost object not created?
kubectl -n opencost get externalcosts -o wide

# CronJob status
kubectl -n llm-d-monitoring get cronjob llmd-external-cost
kubectl -n llm-d-monitoring logs job/$(kubectl -n llm-d-monitoring get jobs --sort-by=.metadata.creationTimestamp -o name | tail -1)
```

---

## One-liner recap

```bash
## 1. run llm-d + Kuadrant + Prometheus (previous README)
## 2. add cost metrics  (previous README)
## 3. install OpenCost  (this section)
## 4. kubectl apply llmd-external-cost cronjob
## 5. results → http://localhost:9003 
```

You now have:

* **Per-call rate-limit enforcement** via Kuadrant/Limitador
* **Real-time spend vectors** in Prometheus
* **Cluster-wide and LLM-specific cost accounting** in OpenCost, ready to feed invoices or dashboards.

## Minikube web UI access - SSH Tunnel

1. **On EC2**, run a localhost-only port-forward:

```shell
kubectl -n opencost port-forward \
  --address 0.0.0.0 \
  svc/opencost 9090:9090
```

```bash
   kubectl -n opencost port-forward svc/opencost 9090:9090
```
2. **On your device**, open an SSH tunnel (replace key and hostname):

```bash
   ssh -i instruct-bot.pem \
     -L 9090:localhost:9090 \
     <USER>@<EC2_PUBLIC_IP> -N
```

3. **Browse locally**

```
   http://localhost:9090
```
