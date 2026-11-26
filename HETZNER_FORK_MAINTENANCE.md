# Hetzner fork maintenance — MANDATORY on every KOF / k0rdent upgrade

> **This is a permanent, LOCAL-only patch. We deliberately do NOT upstream it.**
> Every time KOF (or the k0rdent stack it ships with) is upgraded, this patch
> **must be re-applied** to the new `kof-operator` version, rebuilt, and
> redeployed. If you skip it, multi-tenant KOF on Hetzner silently breaks.

## Why this is required

The upstream `kof-operator-controller` only recognises a fixed set of cloud
providers (AWS, Azure, GCP, OpenStack, VSphere). On **Hetzner** its `getCloud()`
returns `unknown infrastructure provider in ClusterTemplate "hetzner-..."`, so it
**never** creates:

- the regional cloud ConfigMap `kof-<regional>`,
- a child's `kof-cluster-config-<child>` ConfigMap,
- the per-tenant `kof-vmuser-creds-*` Secret / VMUser.

**Symptom when the patch is missing:** a KOF child `ClusterDeployment` sticks at
`Services: N/M` with `kof/kof-operators` + `kof/kof-vmuser-creds` **Pending**, and
the Sveltos `ClusterSummary` shows `referenced resource … does not exist`. No
child telemetry reaches the regional.

We intentionally keep this as a **local fork** rather than a PR to `k0rdent/kof`:
Hetzner is not an upstream-supported provider and we do not want to depend on
upstream review cycles for a stack we ship ourselves. **Assume upstream will
never carry it — always re-apply.**

## The patch (3 files, 9 lines) — commit `0e34339`

Adds `hetzner` to the operator's cloud set, region-based location matching, and
`ConfigData` serialisation. If a cherry-pick conflicts on a new version, apply
this diff by hand:

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

## Upgrade procedure (run this for EVERY new KOF version)

Let `X.Y.Z` be the new upstream KOF version (e.g. `1.11.0`).

```bash
cd K0Cluster/kof

# 1. Fetch upstream tags and branch off the new tag
git fetch origin --tags                      # origin = k0rdent/kof (upstream)
git worktree add -b build/kof-operator-vX.Y.Z-hetzner /tmp/kof-build vX.Y.Z
cd /tmp/kof-build

# 2. Re-apply the Hetzner patch (cherry-pick; fall back to the diff above on conflict)
git cherry-pick 0e34339        # commit lives on enopax/kof
#   ...resolve conflicts by hand using the diff above, then: git cherry-pick --continue

# 3. Copy the build Dockerfile from the previous build branch
git show enopax/build/kof-operator-v1.10.0-hetzner:kof-operator/Dockerfile.hetzner \
  > kof-operator/Dockerfile.hetzner

# 4. Build + push the amd64 image to our private registry
cd kof-operator
docker login ghcr.io                          # gh auth token | docker login ...
docker buildx build --platform linux/amd64 \
  -f Dockerfile.hetzner \
  -t ghcr.io/enopax/kof-operator-controller:vX.Y.Z-hetzner \
  --push .

# 5. Persist the branch on the fork (never push to upstream `origin`)
git add kof-operator/Dockerfile.hetzner && git commit -m "build: Dockerfile for vX.Y.Z-hetzner"
git push enopax build/kof-operator-vX.Y.Z-hetzner
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
            tag: vX.Y.Z-hetzner          # <-- bump this every upgrade
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
# operator no longer errors on Hetzner
kubectl logs deploy/kof-mothership-kof-operator -n kof | grep -i "unknown infrastructure" && echo FAIL || echo OK

# regional + child KOF config regenerated
kubectl get cm  kof-<regional>            -n kcm-system    # e.g. kof-ep-eu1-fsn1-standalone
kubectl get cm  kof-cluster-config-<child> -n kcm-system
kubectl get secret kof-vmuser-creds-*      -n kcm-system

# child telemetry reaches the regional
#   count(up{cluster="<child>"}) on the regional vmselect must return > 0
```

## Related local requirement (not the operator, but same class)

Hosted-CP **child** clusters need cert-manager for `kof-operators` (the otel-operator
webhook uses `Certificate`/`Issuer` CRDs); the hosted-CP template does not bundle it.
Add the `cert-manager-vX` ServiceTemplate to the child `spec.serviceSpec.services`
(see `K0Cluster/cluster/manifests/eu1f-test01.yaml` and runbook blocker #11).
Re-check this stays present after any child-template change.
