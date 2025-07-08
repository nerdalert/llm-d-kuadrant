# Inference Rate Limiting Example Use Cases

Inference rate limiting geared toward Istio-based gateways—broken down into key feature areas:

### **1. Strategic Traffic Control**

* **Burst Protection:** Smooths out sudden spikes from heavy requests or large prompts to prevent GPU queue overloads, resource exhaustion, and service degradation.
* **Latency Stabilization:** Maintains consistent and predictable response times by throttling excess traffic before it can overwhelm the backend model servers.

### **2. Tenant-Level Isolation & Fair Usage**

* **Per-API-Key Quotas:** Assign individual rate limits to specific users, applications, or API tokens to prevent any single client from monopolizing shared GPU resources.
* **Team & Namespace Limits:** Enforce separate consumption caps at a team, project, or Kubernetes namespace scope to guarantee fair access and resource allocation in multi-tenant clusters.

### **3. Consumption-Aware Cost Control**

* **Token-Based Caps:** Apply precise limits based on the number of input and output tokens processed per minute, hour, or day. This aligns rate limits directly with the actual computational cost of a request.
* **Request-Based Caps:** Set simple limits on the number of API calls allowed per time interval, ideal for scenarios where token-level tracking is not required.

### **4. Granular Scope & Policy Dimensions**

* **Model Type:** Differentiate limits based on model size or class (e.g., a 3B vs. a 65B parameter model) to align traffic policies with compute costs and SLOs.
* **Request Category:** Tailor quotas for distinct API functions like chat completions, embeddings, or retrieval-augmented generation (RAG), as each carries a unique resource profile.
* **Geographic or Cluster Region:** Adapt policies for globally distributed deployments, allowing for better load balancing and preventing saturation in specific zones or clusters.

### **5. Rule-Driven Policy Engine**

* **Ordered Matching:** Evaluate rules in a specific sequence, allowing more specific policies (e.g., for priority users) to take precedence over broader, default limits.
* **Metadata Filters:** Use labels or tags (e.g., `environment: staging`) to apply environment-specific caps dynamically without requiring code changes.

### **6. Declarative, Kubernetes-Native Configuration**

* **YAML Configuration:** Define all rate-limiting policies declaratively using standard Kubernetes resources like ConfigMaps or Custom Resource Definitions (CRDs).
* **GitOps-Friendly:** Manage and version-control rate-limiting policies alongside application manifests in a Git repository, enabling auditable and repeatable deployments.

### **7. High-Performance, Low-Latency Enforcement**

* **In-Memory Sidecar Enforcement:** Utilize Envoy's local rate-limit filter (via [Kuadrant/wasm-shim](https://github.com/Kuadrant/wasm-shim)) to enforce quotas directly within the service mesh sidecar, with sub-millisecond decision times that do not impact inference latency.
* **Scalable Backend Integration:** For policies requiring global counters or persistence, the gateway can integrate with external rate-limit services like Redis or any gRPC-based Rate Limit Service (RLS).
