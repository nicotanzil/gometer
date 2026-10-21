# Kubernetes deployment

[Documentation](README.md) · [Project overview](../README.md)

GoMeter can run as a private dashboard coordinator with a fixed group of worker
pods. The dashboard is single-user; this layout is for a trusted operator behind
a VPN or an ingress that enforces authentication and an allowlist. The app does
not provide user accounts or role-based access.

## Pod layout

- Run one dashboard pod as a Deployment with a persistent volume mounted at
  `/data`. Keep it at one replica; workspace files have no cross-process lock.
- Run workers as a fixed-replica StatefulSet behind a headless Service. Use each
  worker pod's stable DNS name as a separate worker origin. A load-balanced
  Service does not provide GoMeter with distinct worker identities.
- Route the private ingress to an internal ClusterIP Service. Terminate browser
  TLS at the ingress, preserve the browser's `Host` and `Origin`, and route from
  `/` without a path prefix. The pod-to-ingress network path should be restricted
  to the ingress controller.
- Give the dashboard and workers the same `GOMETER_WORKER_TOKEN`. Mount worker
  TLS certificates and keys from Secrets. Mount the issuing CA in the dashboard
  and set `GOMETER_WORKER_CA` to its file path.
- Restrict worker ingress to the dashboard. Restrict worker egress to DNS and
  the approved test targets. The targets must be reachable from every worker.

The dashboard command for a private TLS-terminating ingress has this shape:

```sh
gometer ui -listen 0.0.0.0:8790 \
  -public-origin https://gometer.example.internal \
  -data /data
```

The public origin must be the exact HTTPS origin users open in the browser. The
worker certificate must verify for every configured worker DNS name. For a
StatefulSet named `gometer-worker` and a headless Service named `gometer-workers`,
the origins are similar to:

```text
https://gometer-worker-0.gometer-workers.gometer.svc.cluster.local:8801
https://gometer-worker-1.gometer-workers.gometer.svc.cluster.local:8801
https://gometer-worker-2.gometer-workers.gometer.svc.cluster.local:8801
```

Enter one origin per line in the dashboard's **Workers** field. GoMeter does not
discover StatefulSet replicas. If the replica count changes, update the worker
list as well. Distributed workers currently support rate mode only.

## Access and recovery boundaries

The origin check binds requests to the configured ingress host. It is not user
authentication. Require the VPN or ingress to authenticate and allowlist users;
restrict direct access to the dashboard Service so clients cannot bypass that
gate. Do not expose the dashboard directly to the public internet.

Keep the dashboard at one replica with one private persistent volume. Back up the
whole `/data` directory and practice a restore. A worker restart loses its active
job and in-memory result, so a run can finish incomplete with unknown traffic.
Update worker pods between runs and preserve GoMeter's graceful SIGTERM window.

Worker network egress needs deliberate controls because a configured workload
can send traffic to the chosen target. Allow only approved targets and required
DNS. Resource limits also affect generator capacity; record requests/limits and
monitor CPU throttling, memory, and network headroom for every run.

## Example manifests for F5 NGINX Ingress Controller

The repository includes a starting point in [`k8s/base`](../k8s/base) and a
separate [`k8s/f5-kic`](../k8s/f5-kic) overlay. The base creates one dashboard,
a 10 GiB PVC, three fixed workers, their headless Service, and NetworkPolicies
that limit dashboard and worker control traffic. The overlay adds HTTPS ingress
and an F5 NGINX access-control policy.

These files are templates. Before applying them:

1. Build and push a Linux image to a registry your cluster can read. For GitHub
Container Registry, authenticate Docker with a GitHub token that has package
write permission, then build from the repository root:

   ```sh
   docker buildx build --platform linux/amd64 \
     --build-arg VERSION=0.6.0 \
     --tag ghcr.io/nicotanzil/gometer:0.6.0 \
     --push .
   ```

   If your cluster has ARM nodes, use
   `--platform linux/amd64,linux/arm64`. The Dockerfile builds for both
   architectures. For a private GHCR package, configure an image-pull Secret
   for the namespace and reference it from both pod specs.

2. The dashboard and worker manifests use
   `ghcr.io/nicotanzil/gometer:0.6.0`. Change the image in both files if you
   publish a different version. Change `gometer.example.internal` in the
   dashboard arguments and `k8s/f5-kic/ingress.yaml` to your actual HTTPS
   hostname. The default resource requests and limits are starting points;
   measure capacity under your real workloads before relying on them.

3. Replace `192.0.2.0/24` in `k8s/f5-kic/access-policy.yaml` with the source
   CIDR that the F5 controller sees for VPN clients. The documentation range
   intentionally does not match real users, so access stays denied until you
   replace it. Confirm the observed client source address with your cluster
   network setup; NAT or forwarded-client settings can change it. This policy
   requires F5 NGINX Ingress Controller's `k8s.nginx.org/v1` Policy CRD and
   policy support. The `ingressClassName` must match your installed IngressClass.

   The base NetworkPolicy also denies direct dashboard traffic unless both the
   NGINX controller namespace and its pod template carry the label
   `gometer-ingress-controller: "true"`. Inspect your controller's namespace
   and pod labels, then persist this label on its namespace and in its Helm
   values or Deployment template. This makes the Service reachable only from
   the designated controller; the Ingress policy separately limits browser
   clients to the VPN source CIDRs.

4. Prepare certificates. `gometer-dashboard-tls` must cover the dashboard
   hostname. `gometer-worker-tls` must contain a certificate whose SAN covers
   each worker pod DNS name (a wildcard such as
   `*.gometer-workers.gometer.svc.cluster.local` works with the default cluster
   domain). `gometer-worker-ca` contains the CA that issued that worker
   certificate. If your cluster uses a different DNS domain, use it in the
   certificate and in each worker origin entered in the dashboard.

   The image does not include a system CA bundle. The `gometer-worker-ca`
   Secret only verifies dashboard-to-worker connections. Before workers test
   HTTPS targets, provide a trust bundle containing the required public and
   organization roots to the worker pods, or rebuild the image with that
   bundle.

5. Create the namespace and Secrets. Keep these files and token values out of
   Git:

   ```sh
   kubectl apply -f k8s/base/namespace.yaml
   kubectl -n gometer create secret generic gometer-worker-token \
     --from-literal=token="$(openssl rand -hex 32)"
   kubectl -n gometer create secret tls gometer-worker-tls \
     --cert=worker.crt --key=worker.key
   kubectl -n gometer create secret generic gometer-worker-ca \
     --from-file=ca.crt=worker-ca.crt
   kubectl -n gometer create secret tls gometer-dashboard-tls \
     --cert=dashboard.crt --key=dashboard.key
   ```

6. Render the files locally, then install and check the base and ingress:

   ```sh
   kubectl kustomize k8s/base
   kubectl apply -k k8s/base
   kubectl kustomize k8s/f5-kic
   kubectl apply -k k8s/f5-kic
   kubectl -n gometer get pods,services,ingress,networkpolicy
   kubectl -n gometer describe ingress gometer-dashboard
   ```

   Check that F5 reports the Ingress as accepted and the `gometer-dashboard-vpn`
   Policy as valid. DNS for the hostname must point to the ingress controller,
   and the controller must have a route from the VPN. The ingress forwards the
   root path and uses the exact host passed to the dashboard's `-public-origin`.

7. In the dashboard's **Workers** field, enter one URL per worker:

   ```text
   https://gometer-worker-0.gometer-workers.gometer.svc.cluster.local:8801
   https://gometer-worker-1.gometer-workers.gometer.svc.cluster.local:8801
   https://gometer-worker-2.gometer-workers.gometer.svc.cluster.local:8801
   ```

   The pod DNS names must match the worker certificate SANs. If you scale the
   StatefulSet, update this list to match. Do not replace the headless Service
   with a load-balanced worker Service.

The optional F5 overlay uses an access-control policy to allow only the stated
VPN CIDRs. If you use an identity provider instead, configure and test an
F5-supported external-auth policy for your installed controller version before
exposing the Ingress. The app itself has no user accounts. Do not expose it to
the public internet without an access gate.

The sample NetworkPolicies restrict dashboard ingress to labeled controller
pods and worker ingress to the dashboard pod. They do not constrain worker
egress because allowed test targets vary by deployment. Add egress rules for
DNS and the approved target ranges in your cluster. Verify that your cluster's
network plugin enforces NetworkPolicy resources.

See [security](../SECURITY.md), [operations](operations.md), and
[distributed workers](workers.md) for the runtime boundaries and recovery
limits.
