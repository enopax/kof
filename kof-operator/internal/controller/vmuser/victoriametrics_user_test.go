package vmuser

import (
	"context"
	"os"
	"testing"

	kcmv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	vmv1beta1 "github.com/VictoriaMetrics/operator/api/operator/v1beta1"
	"github.com/k0rdent/kof/kof-operator/internal/controller/record"
	"github.com/k0rdent/kof/kof-operator/internal/k8s"
	"github.com/k0rdent/kof/kof-operator/internal/models/labels"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	k8sevents "k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestVMUserManager(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "VMUser Manager Suite")
}

var _ = BeforeSuite(func() {
	record.DefaultRecorder = new(k8sevents.FakeRecorder)
})

func newTestScheme() *runtime.Scheme {
	s := scheme.Scheme
	Expect(kcmv1beta1.AddToScheme(s)).To(Succeed())
	Expect(addoncontrollerv1beta1.AddToScheme(s)).To(Succeed())
	Expect(corev1.AddToScheme(s)).To(Succeed())
	Expect(vmv1beta1.AddToScheme(s)).To(Succeed())
	return s
}

var _ = Describe("Build helper functions", func() {
	DescribeTable("formatLabelParam",
		func(key, value, expected string) {
			Expect(formatLabelParam(key, value)).To(Equal(expected))
		},
		Entry("basic", "tenant", "acme", "tenant=acme"),
		Entry("empty value", "tenant", "", "tenant="),
	)

	DescribeTable("formatFilterParams",
		func(key, value, expected string) {
			Expect(formatFilterParams(key, value)).To(Equal(expected))
		},
		Entry("basic", "tenant", "acme", `"tenant":="acme"`),
		Entry("resource_attr prefix", "resource_attr:tenant", "acme", `"resource_attr:tenant":="acme"`),
	)

	Describe("getLabels", func() {
		It("includes managed-by label", func() {
			l := getLabels(nil)
			Expect(l).To(HaveKeyWithValue(labels.ManagedByLabel, k8s.ManagedByValue))
		})

		It("merges extra labels", func() {
			extra := map[string]string{"foo": "bar", "baz": "qux"}
			l := getLabels(extra)
			Expect(l).To(HaveKeyWithValue(labels.ManagedByLabel, k8s.ManagedByValue))
			Expect(l).To(HaveKeyWithValue("foo", "bar"))
			Expect(l).To(HaveKeyWithValue("baz", "qux"))
		})

		It("extra labels do not override managed-by", func() {
			extra := map[string]string{labels.ManagedByLabel: "something-else"}
			l := getLabels(extra)
			// maps.Copy overwrites, so this documents current behavior
			Expect(l).To(HaveKey(labels.ManagedByLabel))
		})
	})
})

var _ = Describe("a write-only VMUser", func() {
	// Der Zugang eines Kindclusters schreibt und liest nicht. Seine
	// Zugangsdaten liegen im Kindcluster, und dessen kubeconfig geht an den
	// Kunden - alles, was dieser Zugang darf, darf der Kunde. Gelesen wird in
	// der Console, ueber einen eigenen Zugang. Siehe enopax/enopax#45.
	cfg := &VMUserConfig{
		ExtraLabel: &ExtraLabel{Key: "tenant", Value: "acme_web"},
		WriteOnly:  true,
	}

	It("keeps every insert target", func() {
		refs := buildTargetRefs(cfg)

		paths := []string{}
		for _, r := range refs {
			paths = append(paths, r.Paths...)
		}
		Expect(paths).To(ConsistOf(vlAuditInsertPath, vlInsertPath, vmInsertPath, vtInsertPath))
	})

	It("drops every select target", func() {
		refs := buildTargetRefs(cfg)

		for _, r := range refs {
			for _, p := range r.Paths {
				Expect(p).NotTo(Or(
					Equal(vlAuditSelectPath),
					Equal(vlSelectPath),
					Equal(vmSelectPath),
					Equal(vtSelectPath),
				))
			}
		}
	})

	It("still stamps the tenant on write", func() {
		refs := buildTargetRefs(cfg)

		var vmInsert *vmv1beta1.TargetRef
		for i := range refs {
			if refs[i].Paths[0] == vmInsertPath {
				vmInsert = &refs[i]
			}
		}
		Expect(vmInsert).NotTo(BeNil())
		Expect(vmInsert.QueryArgs).To(HaveLen(1))
		Expect(vmInsert.QueryArgs[0].Name).To(Equal("extra_label"))
		Expect(vmInsert.QueryArgs[0].Values).To(ConsistOf("tenant=acme_web"))
	})

	It("leaves a reading user untouched", func() {
		// Der Admin-Zugang des regionalen Clusters liest weiter - die
		// Einschraenkung gilt nur, wo sie gesetzt wird.
		reading := &VMUserConfig{
			ExtraLabel:   &ExtraLabel{Key: "tenant", Value: "acme_web"},
			ExtraFilters: map[string]string{"tenant": "acme_web"},
		}
		Expect(buildTargetRefs(reading)).To(HaveLen(8))
		Expect(buildTargetRefs(cfg)).To(HaveLen(4))
	})
})

var _ = Describe("buildQueryArgs and buildTargetRefs", func() {
	Describe("buildQueryArgs with nil config", func() {
		It("returns zero-value queryArgs", func() {
			a := buildQueryArgs(nil)
			Expect(a.vlSelectArgs).To(BeEmpty())
			Expect(a.vlInsertArgs).To(BeEmpty())
			Expect(a.vmSelectArgs).To(BeEmpty())
			Expect(a.vmInsertArgs).To(BeEmpty())
			Expect(a.vtSelectArgs).To(BeEmpty())
			Expect(a.vtInsertArgs).To(BeEmpty())
		})
	})

	Describe("buildQueryArgs with ExtraLabel", func() {
		It("applies extra_label to vmInsert and vlInsert and vtInsert", func() {
			cfg := &VMUserConfig{
				ExtraLabel: &ExtraLabel{Key: "tenant", Value: "acme"},
			}
			a := buildQueryArgs(cfg)

			Expect(a.vmInsertArgs).To(HaveLen(1))
			Expect(a.vmInsertArgs[0].Name).To(Equal("extra_label"))
			Expect(a.vmInsertArgs[0].Values).To(ConsistOf("tenant=acme"))

			Expect(a.vlInsertArgs).To(HaveLen(1))
			Expect(a.vlInsertArgs[0].Name).To(Equal("extra_fields"))
			Expect(a.vlInsertArgs[0].Values).To(ConsistOf("tenant=acme"))

			Expect(a.vtInsertArgs).To(HaveLen(1))
			Expect(a.vtInsertArgs[0].Name).To(Equal("extra_fields"))
			Expect(a.vtInsertArgs[0].Values).To(ConsistOf("resource_attr:tenant=acme"))

			// Select args should be unaffected
			Expect(a.vmSelectArgs).To(BeEmpty())
			Expect(a.vlSelectArgs).To(BeEmpty())
			Expect(a.vtSelectArgs).To(BeEmpty())
		})
	})

	Describe("buildQueryArgs with ExtraFilters", func() {
		It("applies extra_filters to select args", func() {
			cfg := &VMUserConfig{
				ExtraFilters: map[string]string{"tenant": "acme"},
			}
			a := buildQueryArgs(cfg)

			Expect(a.vlSelectArgs).To(HaveLen(1))
			Expect(a.vlSelectArgs[0].Name).To(Equal("extra_filters"))
			Expect(a.vlSelectArgs[0].Values).To(ConsistOf(`"tenant":="acme"`))

			Expect(a.vtSelectArgs).To(HaveLen(1))
			Expect(a.vtSelectArgs[0].Name).To(Equal("extra_filters"))
			Expect(a.vtSelectArgs[0].Values).To(ConsistOf(`"resource_attr:tenant":="acme"`))

			Expect(a.vmSelectArgs).To(HaveLen(1))
			Expect(a.vmSelectArgs[0].Name).To(Equal("extra_filters"))
			Expect(a.vmSelectArgs[0].Values).To(ConsistOf(`{tenant="acme"}`))

			// Insert args should be unaffected
			Expect(a.vmInsertArgs).To(BeEmpty())
			Expect(a.vlInsertArgs).To(BeEmpty())
			Expect(a.vtInsertArgs).To(BeEmpty())
		})
	})
})

var _ = Describe("VMUser Manager - MCS Update Tests", func() {
	var (
		ctx        context.Context
		fakeClient client.Client
		manager    *Manager
	)

	BeforeEach(func() {
		fakeClient = fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			Build()

		manager = NewManager(fakeClient)
		ctx = context.Background()
	})

	It("should create MCS if it does not exist", func() {
		opts := &CreateOptions{
			Name:      "test-cluster",
			Namespace: "test-ns",
			MCSConfig: &MCSConfig{
				ClusterSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						"role": "regional",
					},
				},
			},
		}

		err := manager.createOrUpdatePropagationMCS(ctx, opts)
		Expect(err).NotTo(HaveOccurred())

		// Verify MCS was created
		mcs := &kcmv1beta1.MultiClusterService{}
		err = fakeClient.Get(ctx, client.ObjectKey{Name: BuildMCSName(opts.Name)}, mcs)
		Expect(err).NotTo(HaveOccurred())
		Expect(mcs.Spec.ClusterSelector.MatchLabels).To(Equal(opts.MCSConfig.ClusterSelector.MatchLabels))
	})

	It("should name a state management provider, as k0rdent 1.13 requires", func() {
		// Die MCS-CRD von k0rdent 1.13 traegt minProperties: 1 an
		// spec.serviceSpec.provider. Ein leeres Feld laesst jedes **neue**
		// Objekt an der Admission scheitern, waehrend die bestehenden
		// weiterlaufen - Kubernetes erzwingt eine neu hinzugekommene Regel an
		// einem unveraenderten Feld nicht. Deshalb faellt es nur beim Anlegen
		// eines Clusters auf, und dann nimmt KOF ihn gar nicht auf:
		// enopax/enopax#55.
		opts := &CreateOptions{
			Name:      "provider-cluster",
			Namespace: "test-ns",
			MCSConfig: &MCSConfig{
				ClusterSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{"role": "regional"},
				},
			},
		}

		err := manager.createOrUpdatePropagationMCS(ctx, opts)
		Expect(err).NotTo(HaveOccurred())

		mcs := &kcmv1beta1.MultiClusterService{}
		err = fakeClient.Get(ctx, client.ObjectKey{Name: BuildMCSName(opts.Name)}, mcs)
		Expect(err).NotTo(HaveOccurred())
		Expect(mcs.Spec.ServiceSpec.Provider.Name).To(Equal("ksm-projectsveltos"))

		// Und die Referenzen muessen **dort** stehen, nicht an der
		// abgekuendigten Stelle: k0rdent 1.13 reicht
		// spec.serviceSpec.templateResourceRefs nicht mehr weiter, das
		// erzeugte ServiceSet traegt dann keine Referenzen, und die Vorlage
		// `{{ removeField "vmuser" ... }}` loest ins Leere auf - lautlos, mit
		// einem Helm-Release, das sich `deployed` nennt und leer ist.
		Expect(mcs.Spec.ServiceSpec.TemplateResourceRefs).To(BeEmpty())
		Expect(mcs.Spec.ServiceSpec.Provider.Config).NotTo(BeNil())
		Expect(string(mcs.Spec.ServiceSpec.Provider.Config.Raw)).To(ContainSubstring("templateResourceRefs"))
		Expect(string(mcs.Spec.ServiceSpec.Provider.Config.Raw)).To(ContainSubstring("vmuser"))
		Expect(string(mcs.Spec.ServiceSpec.Provider.Config.Raw)).To(ContainSubstring("secret"))
	})

	It("should not update MCS if spec has not changed", func() {
		opts := &CreateOptions{
			Name:      "test-cluster",
			Namespace: "test-ns",
			MCSConfig: &MCSConfig{
				ClusterSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						"role": "regional",
					},
				},
			},
		}

		// Create initial MCS
		err := manager.createOrUpdatePropagationMCS(ctx, opts)
		Expect(err).NotTo(HaveOccurred())

		// Get the created MCS
		mcs := &kcmv1beta1.MultiClusterService{}
		err = fakeClient.Get(ctx, client.ObjectKey{Name: BuildMCSName(opts.Name)}, mcs)
		Expect(err).NotTo(HaveOccurred())
		originalResourceVersion := mcs.ResourceVersion

		// Call createOrUpdatePropagationMCS again with same opts
		err = manager.createOrUpdatePropagationMCS(ctx, opts)
		Expect(err).NotTo(HaveOccurred())

		// Verify MCS was not updated (resource version should remain the same)
		mcs = &kcmv1beta1.MultiClusterService{}
		err = fakeClient.Get(ctx, client.ObjectKey{Name: BuildMCSName(opts.Name)}, mcs)
		Expect(err).NotTo(HaveOccurred())
		Expect(mcs.ResourceVersion).To(Equal(originalResourceVersion), "MCS should not be updated when spec has not changed")
	})

	It("should update MCS when the spec changes", func() {
		mcsName := BuildMCSName("test-cluster")

		// Create MCS with initial spec
		initialMCS := &kcmv1beta1.MultiClusterService{
			ObjectMeta: metav1.ObjectMeta{
				Name: mcsName,
			},
			Spec: kcmv1beta1.MultiClusterServiceSpec{
				ClusterSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						"role": "regional",
					},
				},
				ServiceSpec: kcmv1beta1.ServiceSpec{
					Services: []kcmv1beta1.Service{
						{
							Name:      "service1",
							Namespace: "ns1",
							Template:  "template1",
						},
					},
					TemplateResourceRefs: []addoncontrollerv1beta1.TemplateResourceRef{
						{
							Identifier: "resource1",
							Resource: corev1.ObjectReference{
								Name:      "resource1",
								Namespace: "ns1",
							},
						},
					},
				},
			},
		}

		// Create the MCS in the fake client
		err := fakeClient.Create(ctx, initialMCS)
		Expect(err).NotTo(HaveOccurred())

		originalResourceVersion := initialMCS.ResourceVersion

		// Now call manager with completely different spec
		opts := &CreateOptions{
			Name:      "test-cluster",
			Namespace: "updated-ns",
			MCSConfig: &MCSConfig{
				ClusterSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						"role": "child",
					},
				},
			},
		}

		err = manager.createOrUpdatePropagationMCS(ctx, opts)
		Expect(err).NotTo(HaveOccurred())

		// Verify MCS was updated
		updatedMCS := &kcmv1beta1.MultiClusterService{}
		err = fakeClient.Get(ctx, client.ObjectKey{Name: mcsName}, updatedMCS)
		Expect(err).NotTo(HaveOccurred())

		// Check that spec changed
		Expect(updatedMCS.ResourceVersion).NotTo(Equal(originalResourceVersion))
		Expect(updatedMCS.Spec.ClusterSelector.MatchLabels).To(Equal(map[string]string{
			"role": "child",
		}))
		Expect(updatedMCS.Spec.ServiceSpec.Services[0].Name).NotTo(Equal(initialMCS.Spec.ServiceSpec.Services[0].Name))
		Expect(updatedMCS.Spec.ServiceSpec.Services[0].Namespace).NotTo(Equal(initialMCS.Spec.ServiceSpec.Services[0].Namespace))
		Expect(updatedMCS.Spec.ServiceSpec.Services[0].Template).NotTo(Equal(initialMCS.Spec.ServiceSpec.Services[0].Template))
	})
})

var _ = Describe("VMUser Manager - createSecret", func() {
	var (
		ctx        context.Context
		fakeClient client.Client
		manager    *Manager
	)

	BeforeEach(func() {
		fakeClient = fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			Build()
		manager = NewManager(fakeClient)
		ctx = context.Background()
	})

	It("creates secrets in both the given namespace and the kof namespace", func() {
		opts := &CreateOptions{
			Name:      "my-cluster",
			Namespace: "test-ns",
		}

		err := manager.createSecret(ctx, opts)
		Expect(err).NotTo(HaveOccurred())

		secret := &corev1.Secret{}
		err = fakeClient.Get(ctx, client.ObjectKey{Name: BuildSecretName(opts.Name), Namespace: opts.Namespace}, secret)
		Expect(err).NotTo(HaveOccurred())
		Expect(secret.Data[UsernameKey]).To(Equal([]byte(opts.Name)))
		Expect(secret.Data[PasswordKey]).NotTo(BeEmpty())

		kofSecret := &corev1.Secret{}
		err = fakeClient.Get(ctx, client.ObjectKey{Name: BuildSecretName(opts.Name), Namespace: k8s.KofNamespace}, kofSecret)
		Expect(err).NotTo(HaveOccurred())
		Expect(kofSecret.Data[PasswordKey]).To(Equal(secret.Data[PasswordKey]))
	})

	It("preserves the existing password on update", func() {
		opts := &CreateOptions{
			Name:      "my-cluster",
			Namespace: "test-ns",
		}

		Expect(manager.createSecret(ctx, opts)).To(Succeed())

		secret := &corev1.Secret{}
		Expect(fakeClient.Get(ctx, client.ObjectKey{Name: BuildSecretName(opts.Name), Namespace: opts.Namespace}, secret)).To(Succeed())
		originalPassword := string(secret.Data[PasswordKey])

		// Call createSecret again — password must not change
		Expect(manager.createSecret(ctx, opts)).To(Succeed())

		Expect(fakeClient.Get(ctx, client.ObjectKey{Name: BuildSecretName(opts.Name), Namespace: opts.Namespace}, secret)).To(Succeed())
		Expect(string(secret.Data[PasswordKey])).To(Equal(originalPassword))
	})

	It("applies extra labels to the secret", func() {
		opts := &CreateOptions{
			Name:        "my-cluster",
			Namespace:   "test-ns",
			ExtraLabels: map[string]string{"env": "prod"},
		}

		Expect(manager.createSecret(ctx, opts)).To(Succeed())

		secret := &corev1.Secret{}
		Expect(fakeClient.Get(ctx, client.ObjectKey{Name: BuildSecretName(opts.Name), Namespace: opts.Namespace}, secret)).To(Succeed())
		Expect(secret.Labels).To(HaveKeyWithValue("env", "prod"))
		Expect(secret.Labels).To(HaveKeyWithValue(labels.ManagedByLabel, k8s.ManagedByValue))
	})

	It("sets owner reference on the secret when provided", func() {
		ownerRef := &metav1.OwnerReference{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Name:       "owner",
			UID:        "uid-1",
		}
		opts := &CreateOptions{
			Name:           "my-cluster",
			Namespace:      "test-ns",
			OwnerReference: ownerRef,
		}

		Expect(manager.createSecret(ctx, opts)).To(Succeed())

		secret := &corev1.Secret{}
		Expect(fakeClient.Get(ctx, client.ObjectKey{Name: BuildSecretName(opts.Name), Namespace: opts.Namespace}, secret)).To(Succeed())
		Expect(secret.OwnerReferences).To(HaveLen(1))
		Expect(secret.OwnerReferences[0]).To(Equal(*ownerRef))
	})
})

var _ = Describe("VMUser Manager - createOrUpdateVMUser", func() {
	var (
		ctx        context.Context
		fakeClient client.Client
		manager    *Manager
	)

	BeforeEach(func() {
		fakeClient = fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			Build()
		manager = NewManager(fakeClient)
		ctx = context.Background()
	})

	It("creates a VMUser with correct spec", func() {
		opts := &CreateOptions{
			Name:      "my-cluster",
			Namespace: "test-ns",
		}

		Expect(manager.createOrUpdateVMUser(ctx, opts)).To(Succeed())

		vmUser := &vmv1beta1.VMUser{}
		Expect(fakeClient.Get(ctx, client.ObjectKey{Name: BuildVMUserName(opts.Name), Namespace: opts.Namespace}, vmUser)).To(Succeed())
		Expect(*vmUser.Spec.Username).To(Equal(opts.Name))
		Expect(vmUser.Spec.PasswordRef.Key).To(Equal(PasswordKey))
		Expect(vmUser.Spec.PasswordRef.LocalObjectReference.Name).To(Equal(BuildSecretName(opts.Name)))
		Expect(vmUser.Spec.TargetRefs).To(HaveLen(8))
	})

	It("applies extra labels and owner references", func() {
		ownerRef := &metav1.OwnerReference{APIVersion: "v1", Kind: "ConfigMap", Name: "owner", UID: "uid-2"}
		opts := &CreateOptions{
			Name:           "my-cluster",
			Namespace:      "test-ns",
			ExtraLabels:    map[string]string{"tier": "regional"},
			OwnerReference: ownerRef,
		}

		Expect(manager.createOrUpdateVMUser(ctx, opts)).To(Succeed())

		vmUser := &vmv1beta1.VMUser{}
		Expect(fakeClient.Get(ctx, client.ObjectKey{Name: BuildVMUserName(opts.Name), Namespace: opts.Namespace}, vmUser)).To(Succeed())
		Expect(vmUser.Labels).To(HaveKeyWithValue("tier", "regional"))
		Expect(vmUser.OwnerReferences).To(HaveLen(1))
		Expect(vmUser.OwnerReferences[0]).To(Equal(*ownerRef))
	})
})

var _ = Describe("VMUser Manager - Create", func() {
	var (
		ctx        context.Context
		fakeClient client.Client
		manager    *Manager
	)

	BeforeEach(func() {
		fakeClient = fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			Build()
		manager = NewManager(fakeClient)
		ctx = context.Background()
	})

	It("returns an error when name is empty", func() {
		err := manager.Create(ctx, &CreateOptions{Name: "", Namespace: "test-ns"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("name cannot be empty"))
	})

	It("creates all resources when MCSConfig is set", func() {
		opts := &CreateOptions{
			Name:      "my-cluster",
			Namespace: "test-ns",
			MCSConfig: &MCSConfig{
				ClusterSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{"role": "regional"},
				},
			},
		}

		Expect(manager.Create(ctx, opts)).To(Succeed())

		// Secret exists in both namespaces
		secret := &corev1.Secret{}
		Expect(fakeClient.Get(ctx, client.ObjectKey{Name: BuildSecretName(opts.Name), Namespace: opts.Namespace}, secret)).To(Succeed())
		kofSecret := &corev1.Secret{}
		Expect(fakeClient.Get(ctx, client.ObjectKey{Name: BuildSecretName(opts.Name), Namespace: k8s.KofNamespace}, kofSecret)).To(Succeed())

		// VMUser exists
		vmUser := &vmv1beta1.VMUser{}
		Expect(fakeClient.Get(ctx, client.ObjectKey{Name: BuildVMUserName(opts.Name), Namespace: opts.Namespace}, vmUser)).To(Succeed())

		// MCS exists
		mcs := &kcmv1beta1.MultiClusterService{}
		Expect(fakeClient.Get(ctx, client.ObjectKey{Name: BuildMCSName(opts.Name)}, mcs)).To(Succeed())
	})

	It("does not create MCS when MCSConfig is nil", func() {
		opts := &CreateOptions{
			Name:      "my-cluster",
			Namespace: "test-ns",
		}

		Expect(manager.Create(ctx, opts)).To(Succeed())

		mcs := &kcmv1beta1.MultiClusterService{}
		err := fakeClient.Get(ctx, client.ObjectKey{Name: BuildMCSName(opts.Name)}, mcs)
		Expect(err).To(HaveOccurred())
	})

	It("does not create MCS in regionless mode", func() {
		Expect(os.Setenv("KOF_REGIONLESS_ENABLED", "true")).To(Succeed())
		DeferCleanup(func() {
			err := os.Unsetenv("KOF_REGIONLESS_ENABLED")
			Expect(err).NotTo(HaveOccurred())
		})

		opts := &CreateOptions{
			Name:      "my-cluster",
			Namespace: "test-ns",
			MCSConfig: &MCSConfig{
				ClusterSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{"role": "regional"},
				},
			},
		}

		Expect(manager.Create(ctx, opts)).To(Succeed())

		mcs := &kcmv1beta1.MultiClusterService{}
		err := fakeClient.Get(ctx, client.ObjectKey{Name: BuildMCSName(opts.Name)}, mcs)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("VMUser Manager - Delete", func() {
	var (
		ctx        context.Context
		fakeClient client.Client
		manager    *Manager
	)

	BeforeEach(func() {
		fakeClient = fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			Build()
		manager = NewManager(fakeClient)
		ctx = context.Background()
	})

	It("returns an error when name is empty", func() {
		err := manager.Delete(ctx, "", "test-ns")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("name cannot be empty"))
	})

	It("succeeds even when resources do not exist (already deleted)", func() {
		Expect(manager.Delete(ctx, "non-existent", "test-ns")).To(Succeed())
	})

	It("deletes all created resources", func() {
		opts := &CreateOptions{
			Name:      "my-cluster",
			Namespace: "test-ns",
			MCSConfig: &MCSConfig{
				ClusterSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{"role": "regional"},
				},
			},
		}
		Expect(manager.Create(ctx, opts)).To(Succeed())

		Expect(manager.Delete(ctx, opts.Name, opts.Namespace)).To(Succeed())

		vmUser := &vmv1beta1.VMUser{}
		Expect(fakeClient.Get(ctx, client.ObjectKey{Name: BuildVMUserName(opts.Name), Namespace: opts.Namespace}, vmUser)).To(HaveOccurred())

		secret := &corev1.Secret{}
		Expect(fakeClient.Get(ctx, client.ObjectKey{Name: BuildSecretName(opts.Name), Namespace: opts.Namespace}, secret)).To(HaveOccurred())

		kofSecret := &corev1.Secret{}
		Expect(fakeClient.Get(ctx, client.ObjectKey{Name: BuildSecretName(opts.Name), Namespace: k8s.KofNamespace}, kofSecret)).To(HaveOccurred())
	})
})
