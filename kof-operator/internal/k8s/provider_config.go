package k8s

import (
	"encoding/json"
	"fmt"

	kcmv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/k0rdent/kof/kof-operator/internal/env"
)

// providerConfig is the provider-specific configuration k0rdent hands to the
// state management provider. Only the part we need is modelled here; the field
// is free-form JSON on the API side.
type providerConfig struct {
	TemplateResourceRefs []addoncontrollerv1beta1.TemplateResourceRef `json:"templateResourceRefs,omitempty"`
}

// StateManagementProvider builds the provider stanza for a MultiClusterService,
// carrying the management-cluster resources that the service values reference.
//
// Both halves of this are required since k0rdent 1.13, and each fails in its
// own way:
//
//   - spec.serviceSpec.provider carries `minProperties: 1` on the CRD. Left
//     empty, the admission webhook rejects every **new** MultiClusterService -
//     while the existing ones keep running, because Kubernetes does not enforce
//     a newly added validation on a field that does not change. So it only
//     shows up when a cluster is created.
//
//   - spec.serviceSpec.templateResourceRefs is deprecated in favour of
//     .provider.config, and 1.13 no longer passes it on. The generated
//     ServiceSet then carries **no** references, the Sveltos template resolves
//     `{{ removeField "childConfig" ... }}` to nothing, and the propagation
//     Helm release is deployed with empty values. Nothing errors: the release
//     reports `deployed`, the ServiceSet reports ready, and the child cluster
//     silently never receives its KOF configuration or its telemetry
//     credentials.
//
// Measured on 2026-11-26 against k0rdent 1.13.0 with Projectsveltos 1.16.0:
// the addon-controller logs `resource childConfig does not exist` and
// `helm get values` shows `data: ""`. See enopax/enopax#55.
func StateManagementProvider(refs []addoncontrollerv1beta1.TemplateResourceRef) (kcmv1beta1.StateManagementProviderConfig, error) {
	provider := kcmv1beta1.StateManagementProviderConfig{
		Name: env.GetStateManagementProviderName(),
	}

	if len(refs) == 0 {
		return provider, nil
	}

	raw, err := json.Marshal(providerConfig{TemplateResourceRefs: refs})
	if err != nil {
		return provider, fmt.Errorf("failed to marshal provider config: %w", err)
	}
	provider.Config = &apiextv1.JSON{Raw: raw}

	return provider, nil
}
