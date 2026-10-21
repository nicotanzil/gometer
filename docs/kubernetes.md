# Kubernetes deployment

[Documentation](README.md) · [Project overview](../README.md)

GoMeter can run as one dashboard pod, with workers added when distributed load
is needed. The dashboard is single-user; use a VPN or NGINX access controls
that authenticate and allowlist users. It stores scenarios and history as JSON files
on a persistent volume; no database is required.

## Certificates for the dashboard and workers

For a dashboard behind an NGINX VirtualServer that terminates HTTPS,
the dashboard hostname certificate is installed at NGINX. The dashboard pod
serves HTTP and does not need its own TLS certificate or private key:

```text
Browser -- HTTPS --> NGINX -- HTTP :8790 --> dashboard pod
```

Set `GOMETER_UI_PUBLIC_ORIGIN` to the exact HTTPS origin opened in the browser.
This configures Host/Origin validation and secure download cookies; it does not
enable TLS in the dashboard process. Preserve the browser's Host and Origin,
route `/` without a path prefix, and restrict pod access to the ingress
controller. A public DNS name and TLS certificate do not provide user login.

| Connection | Certificate and trust configuration |
| --- | --- |
| Browser to NGINX | Dashboard hostname certificate/key at NGINX, for example the `gometer-dashboard-tls` Secret |
| NGINX to dashboard pod | HTTP on port 8790; no dashboard pod certificate |
| Dashboard to remote workers, when used | Worker server certificate/key; dashboard trusts the issuer through `GOMETER_WORKER_CA` or available system roots |

A dashboard-only install does not need `GOMETER_WORKER_TOKEN`,
`GOMETER_WORKER_CA`, `gometer-worker-token`, `gometer-worker-tls`, or
`gometer-worker-ca`. The full fleet template references these Secrets, so use
[`k8s/dashboard-only`](../k8s/dashboard-only) to start without workers.

## Configure startup through environment variables

Current-source templates use `args: ["ui"]` for the dashboard and
`args: ["worker"]` for workers. Startup settings use **explicit flag → environment
variable → built-in default**. Remove the corresponding flag if an environment
variable should control it.

Dashboard container settings:

```yaml
args: ["ui"]
env:
  - name: GOMETER_UI_LISTEN
    value: "0.0.0.0:8790"
  - name: GOMETER_UI_PUBLIC_ORIGIN
    value: "https://gometer.example.internal"
  - name: GOMETER_UI_DATA
    value: "/data"
```

Worker container settings, when workers are used:

```yaml
args: ["worker"]
env:
  - name: GOMETER_WORKER_LISTEN
    value: "0.0.0.0:8801"
  - name: GOMETER_WORKER_NAME
    valueFrom:
      fieldRef:
        fieldPath: metadata.name
  - name: GOMETER_WORKER_TLS_CERT
    value: "/etc/gometer/worker-tls/tls.crt"
  - name: GOMETER_WORKER_TLS_KEY
    value: "/etc/gometer/worker-tls/tls.key"
  - name: GOMETER_WORKER_TOKEN
    valueFrom:
      secretKeyRef:
        name: gometer-worker-token
        key: token
```

The TLS variables are paths to mounted PEM files. The downward API supplies each
worker's pod name directly; `POD_NAME` and argument substitution are unnecessary.

| Variable | Where to set it | Kubernetes value or purpose |
| --- | --- | --- |
| `GOMETER_UI_LISTEN` | Dashboard | `0.0.0.0:8790` so the Service can reach it |
| `GOMETER_UI_PUBLIC_ORIGIN` | Dashboard | Exact browser origin, such as `https://gometer.example.internal`; no path prefix |
| `GOMETER_UI_DATA` | Dashboard | `/data`, backed by the PVC |
| `GOMETER_WORKER_LISTEN` | Workers | `0.0.0.0:8801`; non-loopback requires TLS |
| `GOMETER_WORKER_NAME` | Workers | `metadata.name` through the downward API |
| `GOMETER_WORKER_TLS_CERT` | Workers | Mounted server certificate PEM path |
| `GOMETER_WORKER_TLS_KEY` | Workers | Mounted private key PEM path |
| `GOMETER_WORKER_TOKEN` | Dashboard and workers, for distributed runs | Same token from `gometer-worker-token` on both sides |
| `GOMETER_WORKER_CA` | Dashboard, for a private worker CA | Mounted issuer CA certificate path, such as `/etc/gometer/worker-ca/ca.crt` |
| `GOMETER_TELEMETRY_TOKEN` | Dashboard, if exporter bearer auth is configured | Exporter credential supplied through a Secret |
| `GOMETER_TELEMETRY_CA` | Dashboard, if the exporter uses a private CA | Mounted exporter CA certificate path |

The current variable prefix is `GOMETER_*`; older `GMETER_*` names are not read.
There is no environment variable that discovers or registers workers. Enter
worker URLs in **Build → Execution & diagnostics → Workers**, one per line, or
in the scenario's `workers` list. `GOMETER_CONFIG` applies to `run` and `validate`;
it does not load a dashboard scenario at startup. See the complete
[environment reference](cli.md#environment).

Configuration can come from a ConfigMap via `envFrom` or `valueFrom`; tokens and
private keys belong in Secrets. GoMeter reads process environment variables,
without loading `.env` files or expanding shell expressions in their values.
Applying a changed pod-template `env` rolls out new pods. After changing values
in an existing ConfigMap or Secret used as environment variables, restart the
relevant workloads between runs:

```sh
kubectl -n gometer rollout restart deployment/gometer-dashboard
# Only for an installed worker fleet:
kubectl -n gometer rollout restart statefulset/gometer-worker
```

Startup variables are read when the process starts. A ConfigMap update does not
replace the environment of a running container. See Kubernetes' guide to
[container environment variables](https://kubernetes.io/docs/tasks/inject-data-application/define-environment-variable-container/).

## Build an image that supports these variables

The templates use `ghcr.io/nicotanzil/gometer:env-startup` as an example tag that
you build and push from this source checkout. It is not a published release
claim. The released `0.6.0` image predates the new startup variables; using it
with `args: ["ui"]` or `args: ["worker"]` ignores those settings and keeps the
old defaults. Build the current source before applying these templates:

```sh
docker buildx build --platform linux/amd64 \
  --build-arg VERSION=0.6.0-dev \
  --tag ghcr.io/nicotanzil/gometer:env-startup \
  --push .
```

Use a registry you can publish to and your cluster can read. For ARM nodes,
select `--platform linux/amd64,linux/arm64`. Replace the image reference in
`k8s/base/dashboard.yaml` and `k8s/base/workers.yaml` if you choose a different
tag; use a new tag or digest for subsequent builds. For a private registry,
configure an image-pull Secret in both pod specs. To retain released `0.6.0`,
continue passing its `-listen`, `-public-origin`, `-data`, `-name`, `-tls-cert`,
and `-tls-key` flags instead of relying on the new startup variables.

## Prepare the VirtualServer and storage

The examples use namespace `gometer`, one dashboard Deployment with a 10 GiB
PVC mounted at `/data`, and an internal ClusterIP Service. Keep the dashboard at
one replica; workspace files have no cross-process lock. Make sure your cluster
can provision the PVC and permit UID/GID 65532 to write to it.

Change `GOMETER_UI_PUBLIC_ORIGIN` in `k8s/base/dashboard.yaml` and the host in
`k8s/f5-kic/virtualserver.yaml` to the same browser origin. Use your installed
IngressClass. DNS must point to the NGINX controller.

The dashboard NetworkPolicy permits port 8790 only from controller pods whose
namespace and pod template both carry `gometer-ingress-controller: "true"`.
Persist that label on the controller namespace and in its Helm values or pod
template. A Service or VirtualServer alone does not restrict direct pod access.
Your network plugin must enforce NetworkPolicy for this rule to have effect.

The supplied VirtualServer routes traffic but does not authenticate users or
allowlist client IPs. Keep the hostname behind your VPN or existing controller
access controls before exposing it. If you already have a VirtualServer for
this hostname, add the dashboard upstream and `/` route there instead of
creating a second VirtualServer with the same host.

## Start the dashboard without workers

For a new install, render and apply the dashboard-only template. It includes
the namespace, PVC, Deployment, ClusterIP Service and dashboard NetworkPolicy,
without worker resources or worker Secret references:

```sh
kubectl kustomize k8s/dashboard-only
kubectl apply -k k8s/dashboard-only
```

If your VirtualServer already has a valid hostname certificate, keep that
certificate at NGINX. Otherwise, create the Secret it will reference:

```sh
kubectl -n gometer create secret tls gometer-dashboard-tls \
  --cert=dashboard.crt --key=dashboard.key
```

Point the VirtualServer's upstream to `gometer-dashboard:8790` over HTTP and
forward `/`. The supplied [`virtualserver.yaml`](../k8s/f5-kic/virtualserver.yaml)
has this shape:

```yaml
apiVersion: k8s.nginx.org/v1
kind: VirtualServer
metadata:
  name: gometer-dashboard
  namespace: gometer
spec:
  ingressClassName: nginx
  host: gometer.example.internal
  tls:
    secret: gometer-dashboard-tls
    redirect:
      enable: true
  upstreams:
    - name: dashboard
      service: gometer-dashboard
      port: 8790
  routes:
    - path: /
      action:
        pass: dashboard
```

If you do not have an existing VirtualServer for this hostname, edit the
supplied file's host and IngressClass, then apply it with
`kubectl -n gometer apply -f k8s/f5-kic/virtualserver.yaml`. Its TLS Secret
must be in the same namespace. This follows the official
[F5 VirtualServer reference](https://docs.nginx.com/nginx-ingress-controller/configuration/virtualserver-and-virtualserverroute-resources/).

Do not apply `k8s/f5-kic` for this dashboard-only path: that overlay includes the
worker fleet. Applying the dashboard-only template does not delete
worker resources from an earlier full-fleet install.

Check the dashboard rollout and the accepted NGINX resource, then open its HTTPS
hostname. Leave **Workers** blank for local execution:

```sh
kubectl -n gometer rollout status deployment/gometer-dashboard
kubectl -n gometer get pods,services,pvc,networkpolicy
kubectl -n gometer describe virtualserver gometer-dashboard
```

## Add the worker fleet

The full [`k8s/base`](../k8s/base) template adds three workers in a StatefulSet,
a headless Service, and a NetworkPolicy for incoming worker traffic. The F5
[`k8s/f5-kic`](../k8s/f5-kic) overlay includes that base plus the supplied
VirtualServer.

Workers need TLS certificates covering their configured pod DNS names. With the
default cluster domain, the example shared worker certificate can cover
`*.gometer-workers.gometer.svc.cluster.local`, or explicitly list each pod DNS
name as a SAN. Mount only the issuing CA's public certificate in the dashboard;
keep the CA private key with the issuer. `GOMETER_WORKER_CA` is a trust file,
not a dashboard client certificate. Remote worker authentication also requires
the same `GOMETER_WORKER_TOKEN` on both sides.

Create the worker Secrets before applying the full template. The token is
created once and the dashboard and workers reference that same Secret:

```sh
kubectl apply -f k8s/base/namespace.yaml
kubectl -n gometer create secret generic gometer-worker-token \
  --from-literal=token="$(openssl rand -hex 32)"
kubectl -n gometer create secret tls gometer-worker-tls \
  --cert=worker.crt --key=worker.key
kubectl -n gometer create secret generic gometer-worker-ca \
  --from-file=ca.crt=worker-ca.crt
```

With your existing VirtualServer, apply `k8s/base` to add workers:

```sh
kubectl kustomize k8s/base
kubectl apply -k k8s/base
```

If you need to create the supplied VirtualServer, apply `k8s/f5-kic` instead;
it already includes the base:

```sh
kubectl kustomize k8s/f5-kic
kubectl apply -k k8s/f5-kic
```

Check the worker StatefulSet rollout and enter one origin per worker in
**Build → Execution & diagnostics → Workers**:

```text
https://gometer-worker-0.gometer-workers.gometer.svc.cluster.local:8801
https://gometer-worker-1.gometer-workers.gometer.svc.cluster.local:8801
https://gometer-worker-2.gometer-workers.gometer.svc.cluster.local:8801
```

The DNS names must match certificate SANs; adjust them if your namespace,
StatefulSet, Service or cluster domain differs. GoMeter does not discover
replicas: update the list when you scale. Use distinct pod origins rather than
one load-balanced worker Service. Distributed workers support rate mode only.

## Trust, updates and recovery

The scratch image does not include a system CA bundle. Starting the dashboard
and displaying the UI does not require one. Executing verified HTTPS workloads
from the dashboard or workers requires the appropriate public/organization root
bundle in those pods. The worker CA Secret verifies only dashboard-to-worker
control connections; it does not configure trust for workload HTTPS targets or
telemetry exporters.

The example NetworkPolicies restrict dashboard ingress to labeled controller
pods and worker ingress to the dashboard. Add egress rules for DNS and approved
test targets; the templates leave egress open because targets vary. Local runs
originate from the dashboard, so its egress also needs to reach approved targets.
The default CPU/memory requests and limits are starting points; measure real
capacity and observe throttling and memory headroom.

Change ConfigMaps, Secrets, certificates and images between runs, then restart
the affected workloads. Preserve the 60-second graceful SIGTERM window. A worker
restart loses its active job and in-memory result; an incomplete run can have
unknown traffic. Back up the whole dashboard `/data` directory and practice a
restore. These templates have not been verified in a live cluster.

See [security](../SECURITY.md), [operations](operations.md), and
[distributed workers](workers.md) for runtime and recovery boundaries.
