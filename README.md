# Phoenix Orchestrator — Autonomous Recovery & Scaling Platform
## Overview
Phoenix Orchestrator is a production-grade, highly resilient Kubernetes operator and GitOps-ready platform. It is designed to proactively detect anomalies such as crash loops, memory leaks, latency spikes, and resource exhaustion.
By continuously evaluating service health metrics, detecting trends, and deploying remedial actions, Phoenix keeps your services alive in unpredictable production environments.

## Architecture & Foundational Platform
1. **Infrastructure**: Multi-node managed EKS and local k3d automated via standard Terraform. 
2. **Intelligent Operators**: Custom Golang-based operators built upon controller-runtime and Argo definitions.
3. **Observability Layer**: Complete OpenTelemetry + Prometheus + Loki integration out of the box to capture telemetry endpoints flawlessly.
4. **Resiliency First**: Integrated network and node pressure sensing, chaos simulations (Litmus), and auto-healing hooks out of the box.

## Components
* `operators/phoenix-operator`: The unified go operator, holding components for Predictor, Analyzer, Healer and Collector.
* `infra/terraform/modules/`: Environment deployment abstractions handling k3d + cluster bootstrapping.
* `services/*`: Sample microservices implementing best-practice Phoenix SLA definitions.

## Getting Started
1. Ensure your Terraform definitions (`infra/terraform`) correspond to the appropriate local `dev` target.
2. Ensure you have properly run `go build` inside `operators/phoenix-operator`.
3. Apply `k8s/argocd/app-of-apps.yaml` to begin the GitOps cycle.

## Documentation
Please reference `docs/architecture` for wider scoping contexts and detailed operator flow models.

## License
MIT License