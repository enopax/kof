# Fork maintenance — MANDATORY on every KOF / k0rdent upgrade

> **This is a permanent, LOCAL-only patch. We deliberately do NOT upstream it.**
> Every time KOF (or the k0rdent stack it ships with) is upgraded, this patch
> **must be re-applied** to the new `kof-operator` version, rebuilt, and
> redeployed. If you skip it, multi-tenant KOF on Hetzner **and** on STACKIT
> silently breaks.

This file replaces `HETZNER_FORK_MAINTENANCE.md`: the patch is no longer about
one provider. It now covers **two** — `hetzner` and `stackit` — and the next one
follows the same three edits.

And it is no longer only about providers. A **second kind** of patch has joined
it: keeping the operator's objects acceptable to the k0rdent API it runs
against. That one is below under *k0rdent API compatibility*, and it has to be
carried forward just as faithfully — its failure mode is worse, because nothing
reports an error.

## Why this is required

The upstream `kof-operator-controller` only recognises a fixed set of cloud
providers (AWS, Azure, Docker, GCP, OpenStack, vSphere). `getCloud()` reads the
provider out of `infrastructure-<name>` in `ClusterTemplate.status.providers`
and only passes it on when `cloud.IsValidName()` knows it. For anything else it
returns `unknown infrastructure provider in ClusterTemplate "..."`, and then it
**never** creates:

- the regional cloud ConfigMap `kof-<regional>`,
- a child's `kof-cluster-config-<child>` ConfigMap,
- the per-tenant `kof-vmuser-creds-*` Secret / VMUser.

**Symptom when the patch is missing:** a KOF child `ClusterDeployment` sticks at
`Services: N/M` with `kof/kof-operators` + `kof/kof-vmuser-creds` **Pending**, and
the Sveltos `ClusterSummary` shows `referenced resource … does not exist`. No
child telemetry reaches the regional. The symptom does not look like "a name is
missing from a list".

We intentionally keep this as a **local fork** rather than a PR to `k0rdent/kof`:
neither Hetzner nor STACKIT is an upstream-supported provider and we do not want
to depend on upstream review cycles for a stack we ship ourselves. **Assume
upstream will never carry it — always re-apply.**

## The tripwire

`kof-operator/internal/controller/cloud/cloud_name_test.go` asserts that both
`hetzner` and `stackit` are valid names. It exists so an upgrade that drops the
patch fails in `make test` instead of failing later as a child cluster whose
telemetry never arrives. If that test is red after a version bump, the patch is
gone — do not "fix" the test.

## The patch — three files per provider

### Hetzner (commit `0e34339`)

```diff
--- a/kof-operator/internal/controller/cloud/cloud_name.go
+++ b/kof-operator/internal/controller/cloud/cloud_name.go
@@ const (
 	GCP       string = "gcp"
+	Hetzner   string = "hetzner"
 	OpenStack string = "openstack"
@@ func IsValidName(cloud string) bool {
-	case AWS, Azure, Docker, GCP, OpenStack, VSphere, Remote, Adopted:
+	case AWS, Azure, Docker, GCP, Hetzner, OpenStack, VSphere, Remote, Adopted:

--- a/kof-operator/internal/controller/clusterdeployment_kof_cluster_role.go
+++ b/kof-operator/internal/controller/clusterdeployment_kof_cluster_role.go
@@ const VSphereDatacenterKey = "vsphere_datacenter"
+const HetznerRegionKey = "hetzner_region"
@@ func locationIsTheSame(...) bool {
 	case cloud.AWS:
 		return c1.Region == c2.Region
+	case cloud.Hetzner:
+		return c1.Region == c2.Region

--- a/kof-operator/internal/controller/configmap_cluster_data.go
+++ b/kof-operator/internal/controller/configmap_cluster_data.go
@@ type ConfigData struct {
 	AzureLocation     string
+	HetznerRegion     string
@@ func NewConfigDataFromClusterDeployment(...) {
 		AzureLocation:     cdConfig.Location,
+		HetznerRegion:     cdConfig.Region,
@@ func NewConfigDataFromConfigMap(...) {
 		AzureLocation:     cm.Data[AzureLocationKey],
+		HetznerRegion:     cm.Data[HetznerRegionKey],
@@ func (c *ConfigData) ToMap() ... {
 		AzureLocationKey:     c.AzureLocation,
+		HetznerRegionKey:     c.HetznerRegion,
```

### STACKIT (enopax/enopax#51)

Same three files, same shape. The provider name comes out as `stackit` because
our cluster charts annotate `cluster.x-k8s.io/provider: infrastructure-stackit`.

```diff
--- a/kof-operator/internal/controller/cloud/cloud_name.go
+++ b/kof-operator/internal/controller/cloud/cloud_name.go
@@ const (
 	Hetzner   string = "hetzner"
+	StackIT   string = "stackit"
 	OpenStack string = "openstack"
@@ func IsValidName(cloud string) bool {
-	case AWS, Azure, Docker, GCP, Hetzner, OpenStack, VSphere, Remote, Adopted:
+	case AWS, Azure, Docker, GCP, Hetzner, StackIT, OpenStack, VSphere, Remote, Adopted:

--- a/kof-operator/internal/controller/clusterdeployment_kof_cluster_role.go
+++ b/kof-operator/internal/controller/clusterdeployment_kof_cluster_role.go
@@ const HetznerRegionKey = "hetzner_region"
+const StackitRegionKey = "stackit_region"
@@ func locationIsTheSame(...) bool {
 	case cloud.Hetzner:
 		return c1.Region == c2.Region
+	case cloud.StackIT:
+		return c1.Region == c2.Region

--- a/kof-operator/internal/controller/configmap_cluster_data.go
+++ b/kof-operator/internal/controller/configmap_cluster_data.go
@@ type ConfigData struct {
 	HetznerRegion     string
+	StackitRegion     string
@@ func NewConfigDataFromClusterDeployment(...) {
 		HetznerRegion:     cdConfig.Region,
+		StackitRegion:     cdConfig.Region,
@@ func NewConfigDataFromConfigMap(...) {
 		HetznerRegion:     cm.Data[HetznerRegionKey],
+		StackitRegion:     cm.Data[StackitRegionKey],
@@ func (c *ConfigData) ToMap() ... {
 		HetznerRegionKey:     c.HetznerRegion,
+		StackitRegionKey:     c.StackitRegion,
```

`locationIsTheSame` compares `region` (`eu01`), **not** the availability zone.
The zone sits on the `StackitMachineTemplate`, and two children in `eu01-1` and
`eu01-2` are in the same place as far as a regional cluster is concerned.

## k0rdent API compatibility (PR enopax/kof#5, image `v1.10.0-hetzner.4`)

This patch is not about a cloud name. It keeps the `MultiClusterService`
objects the operator creates acceptable to k0rdent — and **readable** by
Sveltos. Two edits, both required since k0rdent 1.13:

- `spec.serviceSpec.provider` must name a `StateManagementProvider`. The CRD
  now carries `minProperties: 1`, so an empty provider makes the admission
  webhook reject every **new** object:

  ```
  MultiClusterService "kof-child-config-propagation-7532a4b0" is invalid:
  spec.serviceSpec.provider: Invalid value: 0:
  spec.serviceSpec.provider in body should have at least 1 properties
  ```

- `spec.serviceSpec.templateResourceRefs` is **deprecated** in favour of
  `.provider.config`, and 1.13 no longer passes it on. The generated
  `ServiceSet` then carries no references at all, so the Sveltos template
  `{{ removeField "childConfig" ... }}` resolves to nothing and the propagation
  Helm release is deployed **with empty values**.

The name comes from `KOF_STATE_MANAGEMENT_PROVIDER`, default
`ksm-projectsveltos` — the same name KCM uses internally
(`internal/util/kube.DefaultStateManagementProvider`, not importable from
here). The builder is `k8s.StateManagementProvider()`; all three places that
construct a `ServiceSpec` use it:

```
kof-operator/internal/controller/clusterdeployment_kof_cluster_child_role.go   childConfig
kof-operator/internal/controller/configmap_cluster_regional.go                 vmRules
kof-operator/internal/controller/vmuser/victoriametrics_user.go                vmuser, secret
```

**Why this one is nastier than a missing cloud name.** Nothing errors. The Helm
release reports `deployed`, the `ServiceSet` reports ready, the cluster just
waits — and the only trace is one line in the addon-controller:

```
addon-controller:  "resource childConfig does not exist"
helm get values:   propagation: {data: ""}
```

It also hits **existing** installations, not just new clusters. On 2026-11-26
the same log showed `resource secret does not exist` and `resource vmuser does
not exist` for a 106-day-old propagation. Those keep working only because
Sveltos does not re-render an unchanged profile; the next render would have
written empty values, and the child would have lost its telemetry credentials.

**The tripwire for this patch** is in
`kof-operator/internal/controller/vmuser/victoriametrics_user_test.go`: it
asserts the provider name is set **and** that the refs live in
`provider.config` and not in the deprecated field. `go test ./internal/...`
fails if either half is lost.

### Existing objects are only half-healed by a new image

The operator rewrites `kof-vmuser-propagation-*` (it goes through
`CreateOrUpdate`), but `kof-child-config-propagation-*` and
`kof-vm-rules-propagation-*` are **create-only** (`Create`, and
`IsAlreadyExists` returns nil). After rolling out a patched image, bring those
two onto the new shape **in place** — do not delete them, because deleting
removes the propagated ConfigMap from the regional for as long as it takes the
operator to notice:

```bash
kubectl patch multiclusterservice kof-child-config-propagation-<hash> --type merge -p \
 '{"spec":{"serviceSpec":{"templateResourceRefs":null,"provider":{"name":"ksm-projectsveltos",
   "config":{"templateResourceRefs":[{"identifier":"childConfig","resource":{"apiVersion":"v1",
   "kind":"ConfigMap","name":"kof-cluster-config-<child>","namespace":"kcm-system"}}]}}}}}'
```

Setting the name is allowed on an existing object: it is immutable only **once
set** (`oldSelf == '' || self == oldSelf`).

### What this patch does *not* cover

The four MultiClusterServices created by the KOF **charts** —
`kof-child-cluster`, `kof-regional-cluster`, `kof-namespace*`,
`kof-storage-child-secret-propagation` — set `provider` only for the management
cluster (`selfManagement: true`). They survive an upgrade because Kubernetes
does not enforce a newly added validation on an unchanged field, but a **fresh**
KOF install on k0rdent 1.13 fails at the same admission check. That is a chart
problem and belongs upstream, not in this fork.

## Upgrade procedure (run this for EVERY new KOF version)

Let `X.Y.Z` be the new upstream KOF version (e.g. `1.11.0`).

```bash
cd K0Cluster/kof

# 1. Fetch upstream tags and branch off the new tag
git fetch origin --tags                      # origin = k0rdent/kof (upstream)
git worktree add -b build/kof-operator-vX.Y.Z-enopax /tmp/kof-build vX.Y.Z
cd /tmp/kof-build

# 2. Re-apply ALL patches (cherry-pick; fall back to the diffs above on conflict)
git cherry-pick 0e34339        # Hetzner
git cherry-pick <stackit-sha>  # STACKIT — see enopax/kof, feat/stackit-cloud
git cherry-pick <mcs-sha>      # k0rdent API compatibility — see enopax/kof#5
#   ...resolve conflicts by hand using the diffs above, then: git cherry-pick --continue

# 3. Copy the build Dockerfile from the previous build branch
git show enopax/build/kof-operator-v1.10.0-hetzner:kof-operator/Dockerfile.hetzner \
  > kof-operator/Dockerfile.hetzner

# 4. Prove the patches are there before building
cd kof-operator
make envtest                    # once: downloads the envtest binaries
make test                       # cloud_name_test.go AND the vmuser suite must be green

# 5. Build + push the amd64 image to our private registry
docker login ghcr.io                          # gh auth token | docker login ...
docker buildx build --platform linux/amd64 \
  -f Dockerfile.hetzner \
  -t ghcr.io/enopax/kof-operator-controller:vX.Y.Z-enopax \
  --push .

# 6. Persist the branch on the fork (never push to upstream `origin`)
git add kof-operator/Dockerfile.hetzner && git commit -m "build: Dockerfile for vX.Y.Z-enopax"
git push enopax build/kof-operator-vX.Y.Z-enopax
```

Then wire the new image into the KOF install (`K0Cluster/cluster`, see the
regional runbook `kof-values.yaml`) and roll it out:

```yaml
# kof-values.yaml
kof-mothership:
  values:
    kcm:
      kof:
        operator:
          image:
            registry: ghcr.io/enopax
            repository: kof-operator-controller
            tag: vX.Y.Z-enopax           # <-- bump this every upgrade
          imagePullSecrets:
            - name: ghcr-pull
```

```bash
# ghcr-pull secret must exist in the kof namespace (private image)
kubectl create secret docker-registry ghcr-pull -n kof \
  --docker-server=ghcr.io --docker-username=<gh-user> --docker-password=<gh-token>
# then: helm upgrade -i kof ... -f kof-values.yaml   (or patch the HelmRelease values)
```

## Verify after every upgrade

```bash
# operator no longer errors on either provider
kubectl logs deploy/kof-mothership-kof-operator -n kof | grep -i "unknown infrastructure" && echo FAIL || echo OK

# regional + child KOF config regenerated
kubectl get cm  kof-<regional>            -n kcm-system    # e.g. kof-ep-eu1-fsn1-standalone
kubectl get cm  kof-cluster-config-<child> -n kcm-system
kubectl get secret kof-vmuser-creds-*      -n kcm-system

# child telemetry reaches the regional
#   count(up{cluster="<child>"}) on the regional vmselect must return > 0

# the MCS objects the operator writes carry a provider AND their refs in it
kubectl get multiclusterservice -o json | jq -r \
  '.items[] | select(.metadata.labels["app.kubernetes.io/managed-by"] == "kof-operator")
   | "\(.metadata.name) \(.spec.serviceSpec.provider.name // "KEIN PROVIDER") \(
       (.spec.serviceSpec.provider.config.templateResourceRefs | length) // 0) refs"'

# and the propagation really carried something (the empty-values failure is silent)
helm get values kof-child-config-<hash> -n kof     # propagation.data must NOT be ""
```

The last two are the ones that catch the silent failure. A `ServiceSet` that
says ready and a Helm release that says `deployed` prove nothing here.

## Related local requirement (not the operator, but same class)

Hosted-CP **child** clusters need cert-manager for `kof-operators` (the otel-operator
webhook uses `Certificate`/`Issuer` CRDs); the hosted-CP template does not bundle it.
Add the `cert-manager-vX` ServiceTemplate to the child `spec.serviceSpec.services`
(see `K0Cluster/cluster/manifests/eu1f-test01.yaml` and runbook blocker #11).
`provider-cluster` does this for both hosted dialects — Hetzner and STACKIT — in
`buildHostedServiceSpec`. Re-check it stays present after any child-template change.
