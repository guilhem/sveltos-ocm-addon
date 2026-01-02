/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	authv1beta1 "open-cluster-management.io/managed-serviceaccount/apis/authentication/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sveltosv1alpha1 "github.com/guilhem/sveltos-ocm-addon/api/v1alpha1"
)

var _ = Describe("SveltosOCMCluster Controller", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("Unit tests for helper functions", func() {
		Describe("createKubeconfig", func() {
			It("should create a valid kubeconfig", func() {
				apiServerURL := "https://api.cluster1.example.com:6443"
				ca := []byte("fake-ca-cert")
				token := []byte("fake-token")
				clusterName := "test-cluster"

				kubeconfigBytes, err := createKubeconfig(apiServerURL, ca, token, clusterName)
				Expect(err).NotTo(HaveOccurred())
				Expect(kubeconfigBytes).NotTo(BeEmpty())

				// Parse the kubeconfig to verify its structure
				config, err := clientcmd.Load(kubeconfigBytes)
				Expect(err).NotTo(HaveOccurred())

				// Verify cluster configuration
				Expect(config.Clusters).To(HaveKey(clusterName))
				Expect(config.Clusters[clusterName].Server).To(Equal(apiServerURL))
				Expect(config.Clusters[clusterName].CertificateAuthorityData).To(Equal(ca))

				// Verify auth info
				Expect(config.AuthInfos).To(HaveKey(clusterName))
				Expect(config.AuthInfos[clusterName].Token).To(Equal(string(token)))

				// Verify context
				Expect(config.Contexts).To(HaveKey(clusterName))
				Expect(config.Contexts[clusterName].Cluster).To(Equal(clusterName))
				Expect(config.Contexts[clusterName].AuthInfo).To(Equal(clusterName))

				// Verify current context
				Expect(config.CurrentContext).To(Equal(clusterName))
			})

			It("should handle special characters in cluster name", func() {
				apiServerURL := "https://api.example.com:6443"
				ca := []byte("ca-data")
				token := []byte("token-data")
				clusterName := "cluster-with-dashes_and_underscores"

				kubeconfigBytes, err := createKubeconfig(apiServerURL, ca, token, clusterName)
				Expect(err).NotTo(HaveOccurred())

				config, err := clientcmd.Load(kubeconfigBytes)
				Expect(err).NotTo(HaveOccurred())
				Expect(config.CurrentContext).To(Equal(clusterName))
			})
		})

		Describe("defaultSveltosKubeconfigName", func() {
			It("should generate correct kubeconfig secret name", func() {
				clusterName := "my-cluster"
				expected := "my-cluster-sveltos-kubeconfig"
				Expect(defaultSveltosKubeconfigName(clusterName)).To(Equal(expected))
			})

			It("should handle empty cluster name", func() {
				clusterName := ""
				expected := "-sveltos-kubeconfig"
				Expect(defaultSveltosKubeconfigName(clusterName)).To(Equal(expected))
			})
		})
	})

	Context("Reconciler tests", func() {
		var (
			reconciler     *SveltosOCMClusterReconciler
			testNamespace  string
			clusterNS      string
			resourceName   string
			namespacedName types.NamespacedName
		)

		// Helper function to set ManagedClusterAddOn status to available
		setAddonAvailable := func(addonName string, namespace string) {
			addon := &addonv1alpha1.ManagedClusterAddOn{}
			addonKey := types.NamespacedName{Name: addonName, Namespace: namespace}
			Expect(k8sClient.Get(ctx, addonKey, addon)).To(Succeed())

			addon.Status.Conditions = []metav1.Condition{
				{
					Type:               addonv1alpha1.ManagedClusterAddOnConditionAvailable,
					Status:             metav1.ConditionTrue,
					Reason:             "ManagedClusterAddOnAvailable",
					Message:            "ManagedClusterAddOn is available",
					ObservedGeneration: addon.Generation,
					LastTransitionTime: metav1.Now(),
				},
			}
			Expect(k8sClient.Status().Update(ctx, addon)).To(Succeed())
		}

		BeforeEach(func() {
			reconciler = &SveltosOCMClusterReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// Use unique names for each test
			testNamespace = "default"
			clusterNS = "cluster1"
			resourceName = "test-sveltos-ocm-cluster"
			namespacedName = types.NamespacedName{
				Name:      resourceName,
				Namespace: testNamespace,
			}

			// Create test namespaces (cluster namespace is needed for OCM resources)
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: clusterNS},
			}
			err := k8sClient.Create(ctx, namespace)
			if err != nil && !apierrors.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}

			// Create open-cluster-management-addon namespace for cluster-proxy
			addonNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management-addon"},
			}
			_ = k8sClient.Create(ctx, addonNamespace) // Ignore if it already exists

			// Create cluster-proxy-user-serving-cert secret in open-cluster-management-addon namespace
			// This is required by createOrUpdateSveltosCluster to get the CA cert
			proxyCASecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-proxy-user-serving-cert",
					Namespace: "open-cluster-management-addon",
				},
				Data: map[string][]byte{
					"ca.crt": []byte("-----BEGIN CERTIFICATE-----\nMIIC...fake...cert\n-----END CERTIFICATE-----"),
				},
			}
			_ = k8sClient.Create(ctx, proxyCASecret) // Ignore if it already exists

			// Create ClusterManagementAddOn for managed-serviceaccount addon on hub
			// This is required by checkManagedServiceAccountAddon
			cma := &addonv1alpha1.ClusterManagementAddOn{
				ObjectMeta: metav1.ObjectMeta{
					Name: "managed-serviceaccount",
				},
			}
			_ = k8sClient.Create(ctx, cma) // Ignore if it already exists
		})

		AfterEach(func() {
			// Cleanup SveltosOCMCluster
			resource := &sveltosv1alpha1.SveltosOCMCluster{}
			err := k8sClient.Get(ctx, namespacedName, resource)
			if err == nil {
				// Remove finalizer to allow deletion
				controllerutil.RemoveFinalizer(resource, FinalizerName)
				_ = k8sClient.Update(ctx, resource)
				_ = k8sClient.Delete(ctx, resource)
			}

			// Cleanup ManagedClusterAddOn
			addon := &addonv1alpha1.ManagedClusterAddOn{}
			addonKey := types.NamespacedName{Name: AddonName, Namespace: clusterNS}
			if err := k8sClient.Get(ctx, addonKey, addon); err == nil {
				_ = k8sClient.Delete(ctx, addon)
			}

			// Cleanup ManagedServiceAccount
			msa := &authv1beta1.ManagedServiceAccount{}
			msaKey := types.NamespacedName{Name: ManagedServiceAccountName, Namespace: clusterNS}
			if err := k8sClient.Get(ctx, msaKey, msa); err == nil {
				_ = k8sClient.Delete(ctx, msa)
			}

			// Cleanup ManagedCluster
			mc := &clusterv1.ManagedCluster{}
			mcKey := types.NamespacedName{Name: clusterNS}
			if err := k8sClient.Get(ctx, mcKey, mc); err == nil {
				_ = k8sClient.Delete(ctx, mc)
			}

			// Cleanup SveltosCluster
			sc := &libsveltosv1beta1.SveltosCluster{}
			scKey := types.NamespacedName{Name: clusterNS, Namespace: clusterNS}
			if err := k8sClient.Get(ctx, scKey, sc); err == nil {
				_ = k8sClient.Delete(ctx, sc)
			}

			// Cleanup secrets
			secret := &corev1.Secret{}
			secretKey := types.NamespacedName{Name: defaultSveltosKubeconfigName(clusterNS), Namespace: clusterNS}
			if err := k8sClient.Get(ctx, secretKey, secret); err == nil {
				_ = k8sClient.Delete(ctx, secret)
			}

			tokenSecret := &corev1.Secret{}
			tokenSecretKey := types.NamespacedName{Name: "sveltos-ocm-token", Namespace: clusterNS}
			if err := k8sClient.Get(ctx, tokenSecretKey, tokenSecret); err == nil {
				_ = k8sClient.Delete(ctx, tokenSecret)
			}
		})

		Describe("When SveltosOCMCluster does not exist", func() {
			It("should return without error", func() {
				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      "non-existent",
						Namespace: testNamespace,
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
			})
		})

		Describe("When SveltosOCMCluster is created", func() {
			It("should add finalizer on first reconciliation", func() {
				By("Creating SveltosOCMCluster resource")
				resource := &sveltosv1alpha1.SveltosOCMCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: testNamespace,
					},
					Spec: sveltosv1alpha1.SveltosOCMClusterSpec{
						LabelSync: true,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				By("Reconciling the resource")
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: namespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				By("Verifying finalizer was added")
				Eventually(func() bool {
					err := k8sClient.Get(ctx, namespacedName, resource)
					if err != nil {
						return false
					}
					return controllerutil.ContainsFinalizer(resource, FinalizerName)
				}, timeout, interval).Should(BeTrue())
			})

			It("should set NoClusters condition when no ManagedClusterAddOns exist", func() {
				By("Creating SveltosOCMCluster resource")
				resource := &sveltosv1alpha1.SveltosOCMCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: testNamespace,
					},
					Spec: sveltosv1alpha1.SveltosOCMClusterSpec{},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				By("Reconciling the resource")
				// First reconcile adds finalizer
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: namespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				// Second reconcile updates status
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: namespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				By("Verifying status condition")
				Eventually(func() bool {
					err := k8sClient.Get(ctx, namespacedName, resource)
					if err != nil {
						return false
					}
					for _, cond := range resource.Status.Conditions {
						if cond.Type == "Available" &&
							cond.Status == metav1.ConditionFalse &&
							cond.Reason == "NoClusters" {
							return true
						}
					}
					return false
				}, timeout, interval).Should(BeTrue())
			})
		})

		Describe("Full reconciliation flow", func() {
			It("should create ManagedServiceAccount and wait for token", func() {
				By("Creating prerequisites")
				// Create SveltosOCMCluster
				resource := &sveltosv1alpha1.SveltosOCMCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: testNamespace,
					},
					Spec: sveltosv1alpha1.SveltosOCMClusterSpec{
						TokenValidity: metav1.Duration{Duration: 168 * time.Hour},
						LabelSync:     true,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				// Create sveltos-ocm-addon ManagedClusterAddOn
				addon := &addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Name:      AddonName,
						Namespace: clusterNS,
					},
				}
				Expect(k8sClient.Create(ctx, addon)).To(Succeed())

				// Set sveltos-ocm-addon to available status
				setAddonAvailable(AddonName, clusterNS)

				By("Running first reconciliation (adds finalizer and creates managed-serviceaccount ManagedClusterAddOn)")
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: namespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				// Set managed-serviceaccount addon to available
				setAddonAvailable("managed-serviceaccount", clusterNS)

				By("Running second reconciliation (creates ManagedServiceAccount)")
				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: namespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
				// Should requeue to wait for token
				Expect(result.RequeueAfter).To(BeNumerically(">", 0))

				By("Verifying ManagedServiceAccount was created")
				msa := &authv1beta1.ManagedServiceAccount{}
				msaKey := types.NamespacedName{Name: ManagedServiceAccountName, Namespace: clusterNS}
				Expect(k8sClient.Get(ctx, msaKey, msa)).To(Succeed())
				Expect(msa.Spec.Rotation.Enabled).To(BeTrue())
			})

			It("should create SveltosCluster when token is ready", func() {
				By("Creating full test setup")
				// Create SveltosOCMCluster
				resource := &sveltosv1alpha1.SveltosOCMCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: testNamespace,
					},
					Spec: sveltosv1alpha1.SveltosOCMClusterSpec{
						TokenValidity: metav1.Duration{Duration: 168 * time.Hour},
						LabelSync:     true,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				// Create sveltos-ocm-addon ManagedClusterAddOn
				addon := &addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Name:      AddonName,
						Namespace: clusterNS,
					},
				}
				Expect(k8sClient.Create(ctx, addon)).To(Succeed())

				// Set sveltos-ocm-addon to available status
				setAddonAvailable(AddonName, clusterNS)

				// Create ManagedCluster with API server URL
				managedCluster := &clusterv1.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: clusterNS,
						Labels: map[string]string{
							"environment": "test",
							"region":      "us-east-1",
						},
					},
					Spec: clusterv1.ManagedClusterSpec{
						ManagedClusterClientConfigs: []clusterv1.ClientConfig{
							{
								URL: "https://api.cluster1.example.com:6443",
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, managedCluster)).To(Succeed())

				// Create token secret
				tokenSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sveltos-ocm-token",
						Namespace: clusterNS,
					},
					Data: map[string][]byte{
						"token":  []byte("test-token"),
						"ca.crt": []byte("test-ca-cert"),
					},
				}
				Expect(k8sClient.Create(ctx, tokenSecret)).To(Succeed())

				// Create ManagedServiceAccount with token reference
				msa := &authv1beta1.ManagedServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      ManagedServiceAccountName,
						Namespace: clusterNS,
					},
					Spec: authv1beta1.ManagedServiceAccountSpec{
						Rotation: authv1beta1.ManagedServiceAccountRotation{
							Enabled: true,
						},
					},
				}
				Expect(k8sClient.Create(ctx, msa)).To(Succeed())

				// Update ManagedServiceAccount status with token reference
				msa.Status.TokenSecretRef = &authv1beta1.SecretRef{
					Name:                 tokenSecret.Name,
					LastRefreshTimestamp: metav1.Now(),
				}
				msa.Status.ExpirationTimestamp = &metav1.Time{
					Time: time.Now().Add(168 * time.Hour),
				}
				Expect(k8sClient.Status().Update(ctx, msa)).To(Succeed())

				By("Running reconciliation")
				// First reconcile adds finalizer
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: namespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				// Set managed-serviceaccount addon to available
				setAddonAvailable("managed-serviceaccount", clusterNS)

				// Second reconcile processes the cluster
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: namespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				By("Verifying SveltosCluster was created")
				sveltosCluster := &libsveltosv1beta1.SveltosCluster{}
				scKey := types.NamespacedName{Name: clusterNS, Namespace: clusterNS}
				Expect(k8sClient.Get(ctx, scKey, sveltosCluster)).To(Succeed())
				Expect(sveltosCluster.Spec.KubeconfigName).To(Equal(defaultSveltosKubeconfigName(clusterNS)))
				Expect(sveltosCluster.Spec.KubeconfigKeyName).To(Equal("kubeconfig"))

				By("Verifying labels were synced")
				Expect(sveltosCluster.Labels).To(HaveKeyWithValue("environment", "test"))
				Expect(sveltosCluster.Labels).To(HaveKeyWithValue("region", "us-east-1"))

				By("Verifying kubeconfig secret was created")
				kubeconfigSecret := &corev1.Secret{}
				secretKey := types.NamespacedName{
					Name:      defaultSveltosKubeconfigName(clusterNS),
					Namespace: clusterNS,
				}
				Expect(k8sClient.Get(ctx, secretKey, kubeconfigSecret)).To(Succeed())
				Expect(kubeconfigSecret.Data).To(HaveKey("kubeconfig"))

				// Verify kubeconfig content is valid
				kubeconfigData := kubeconfigSecret.Data["kubeconfig"]
				config, err := clientcmd.Load(kubeconfigData)
				Expect(err).NotTo(HaveOccurred())
				// Kubeconfig should use cluster-proxy URL since controller uses cluster-proxy for CAPI clusters
				Expect(config.Clusters[clusterNS].Server).To(Equal("https://cluster-proxy-addon-user.open-cluster-management-addon.svc:9092/cluster1"))

				By("Verifying status was updated")
				Expect(k8sClient.Get(ctx, namespacedName, resource)).To(Succeed())
				Expect(resource.Status.RegisteredClusters).To(HaveLen(1))
				Expect(resource.Status.RegisteredClusters[0].ClusterName).To(Equal(clusterNS))
				Expect(resource.Status.RegisteredClusters[0].SveltosClusterCreated).To(BeTrue())
			})
		})

		Describe("Label sync behavior", func() {
			It("should not sync labels when LabelSync is false", func() {
				By("Creating test setup with LabelSync disabled")
				resource := &sveltosv1alpha1.SveltosOCMCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: testNamespace,
					},
					Spec: sveltosv1alpha1.SveltosOCMClusterSpec{
						LabelSync: false,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				// Create ManagedClusterAddOn
				addon := &addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Name:      AddonName,
						Namespace: clusterNS,
					},
				}
				Expect(k8sClient.Create(ctx, addon)).To(Succeed())

				// Set addon to available status
				setAddonAvailable(AddonName, clusterNS)

				// Create ManagedCluster with labels
				managedCluster := &clusterv1.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: clusterNS,
						Labels: map[string]string{
							"should-not-sync": "true",
						},
					},
					Spec: clusterv1.ManagedClusterSpec{
						ManagedClusterClientConfigs: []clusterv1.ClientConfig{
							{URL: "https://api.cluster1.example.com:6443"},
						},
					},
				}
				Expect(k8sClient.Create(ctx, managedCluster)).To(Succeed())

				// Create token secret
				tokenSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sveltos-ocm-token",
						Namespace: clusterNS,
					},
					Data: map[string][]byte{
						"token":  []byte("test-token"),
						"ca.crt": []byte("test-ca-cert"),
					},
				}
				Expect(k8sClient.Create(ctx, tokenSecret)).To(Succeed())

				// Create ManagedServiceAccount with token
				msa := &authv1beta1.ManagedServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      ManagedServiceAccountName,
						Namespace: clusterNS,
					},
				}
				Expect(k8sClient.Create(ctx, msa)).To(Succeed())
				msa.Status.TokenSecretRef = &authv1beta1.SecretRef{
					Name:                 tokenSecret.Name,
					LastRefreshTimestamp: metav1.Now(),
				}
				Expect(k8sClient.Status().Update(ctx, msa)).To(Succeed())

				By("Running reconciliation")
				_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

				// Set managed-serviceaccount addon to available
				setAddonAvailable("managed-serviceaccount", clusterNS)

				_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

				By("Verifying labels were NOT synced")
				sveltosCluster := &libsveltosv1beta1.SveltosCluster{}
				scKey := types.NamespacedName{Name: clusterNS, Namespace: clusterNS}
				Expect(k8sClient.Get(ctx, scKey, sveltosCluster)).To(Succeed())
				Expect(sveltosCluster.Labels).NotTo(HaveKey("should-not-sync"))
			})
		})

		Describe("Deletion behavior", func() {
			It("should clean up resources when SveltosOCMCluster is deleted", func() {
				By("Creating full test setup")
				resource := &sveltosv1alpha1.SveltosOCMCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: testNamespace,
					},
					Spec: sveltosv1alpha1.SveltosOCMClusterSpec{},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				// Create ManagedClusterAddOn
				addon := &addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Name:      AddonName,
						Namespace: clusterNS,
					},
				}
				Expect(k8sClient.Create(ctx, addon)).To(Succeed())

				// Set addon to available status
				setAddonAvailable(AddonName, clusterNS)

				// Create ManagedCluster
				managedCluster := &clusterv1.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: clusterNS},
					Spec: clusterv1.ManagedClusterSpec{
						ManagedClusterClientConfigs: []clusterv1.ClientConfig{
							{URL: "https://api.cluster1.example.com:6443"},
						},
					},
				}
				Expect(k8sClient.Create(ctx, managedCluster)).To(Succeed())

				// Create token secret
				tokenSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sveltos-ocm-token",
						Namespace: clusterNS,
					},
					Data: map[string][]byte{
						"token":  []byte("test-token"),
						"ca.crt": []byte("test-ca-cert"),
					},
				}
				Expect(k8sClient.Create(ctx, tokenSecret)).To(Succeed())

				// Create ManagedServiceAccount
				msa := &authv1beta1.ManagedServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      ManagedServiceAccountName,
						Namespace: clusterNS,
					},
				}
				Expect(k8sClient.Create(ctx, msa)).To(Succeed())
				msa.Status.TokenSecretRef = &authv1beta1.SecretRef{
					Name:                 tokenSecret.Name,
					LastRefreshTimestamp: metav1.Now(),
				}
				Expect(k8sClient.Status().Update(ctx, msa)).To(Succeed())

				By("Running reconciliation to create resources")
				_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

				// Set managed-serviceaccount addon to available
				setAddonAvailable("managed-serviceaccount", clusterNS)

				_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

				By("Verifying resources were created")
				sveltosCluster := &libsveltosv1beta1.SveltosCluster{}
				scKey := types.NamespacedName{Name: clusterNS, Namespace: clusterNS}
				Expect(k8sClient.Get(ctx, scKey, sveltosCluster)).To(Succeed())

				kubeconfigSecret := &corev1.Secret{}
				secretKey := types.NamespacedName{
					Name:      defaultSveltosKubeconfigName(clusterNS),
					Namespace: clusterNS,
				}
				Expect(k8sClient.Get(ctx, secretKey, kubeconfigSecret)).To(Succeed())

				By("Deleting SveltosOCMCluster")
				Expect(k8sClient.Get(ctx, namespacedName, resource)).To(Succeed())
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

				By("Running deletion reconciliation")
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
				Expect(err).NotTo(HaveOccurred())

				By("Verifying SveltosCluster was deleted")
				Eventually(func() bool {
					err := k8sClient.Get(ctx, scKey, sveltosCluster)
					return apierrors.IsNotFound(err)
				}, timeout, interval).Should(BeTrue())

				By("Verifying kubeconfig secret was deleted")
				Eventually(func() bool {
					err := k8sClient.Get(ctx, secretKey, kubeconfigSecret)
					return apierrors.IsNotFound(err)
				}, timeout, interval).Should(BeTrue())

				By("Verifying ManagedServiceAccount was deleted")
				Eventually(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      ManagedServiceAccountName,
						Namespace: clusterNS,
					}, msa)
					return apierrors.IsNotFound(err)
				}, timeout, interval).Should(BeTrue())

				By("Verifying finalizer was removed")
				err = k8sClient.Get(ctx, namespacedName, resource)
				Expect(apierrors.IsNotFound(err)).To(BeTrue())
			})
		})

		Describe("Error handling", func() {
			It("should handle missing token in secret", func() {
				By("Creating test setup with invalid token secret")
				resource := &sveltosv1alpha1.SveltosOCMCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: testNamespace,
					},
					Spec: sveltosv1alpha1.SveltosOCMClusterSpec{},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				addon := &addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Name:      AddonName,
						Namespace: clusterNS,
					},
				}
				Expect(k8sClient.Create(ctx, addon)).To(Succeed())

				managedCluster := &clusterv1.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: clusterNS},
					Spec: clusterv1.ManagedClusterSpec{
						ManagedClusterClientConfigs: []clusterv1.ClientConfig{
							{URL: "https://api.cluster1.example.com:6443"},
						},
					},
				}
				Expect(k8sClient.Create(ctx, managedCluster)).To(Succeed())

				// Create secret without token key
				invalidSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "invalid-token-secret",
						Namespace: clusterNS,
					},
					Data: map[string][]byte{
						"ca.crt": []byte("test-ca-cert"),
						// Missing "token" key
					},
				}
				Expect(k8sClient.Create(ctx, invalidSecret)).To(Succeed())

				msa := &authv1beta1.ManagedServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      ManagedServiceAccountName,
						Namespace: clusterNS,
					},
				}
				Expect(k8sClient.Create(ctx, msa)).To(Succeed())
				msa.Status.TokenSecretRef = &authv1beta1.SecretRef{
					Name:                 invalidSecret.Name,
					LastRefreshTimestamp: metav1.Now(),
				}
				Expect(k8sClient.Status().Update(ctx, msa)).To(Succeed())

				By("Running reconciliation")
				_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
				result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

				// Should error due to missing token, and requeue
				Expect(err).NotTo(HaveOccurred()) // Error is logged but reconciliation continues
				Expect(result.RequeueAfter).To(BeNumerically(">", 0))

				// Cleanup invalid secret
				_ = k8sClient.Delete(ctx, invalidSecret)
			})

			It("should handle missing cluster-proxy cert", func() {
				By("Creating test setup with missing cluster-proxy cert")
				resource := &sveltosv1alpha1.SveltosOCMCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: testNamespace,
					},
					Spec: sveltosv1alpha1.SveltosOCMClusterSpec{},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				addon := &addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Name:      AddonName,
						Namespace: clusterNS,
					},
				}
				Expect(k8sClient.Create(ctx, addon)).To(Succeed())

				// Set addon to available status
				setAddonAvailable(AddonName, clusterNS)

				// Delete the cluster-proxy-user-serving-cert to simulate missing prerequisite
				proxyCASecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "cluster-proxy-user-serving-cert",
						Namespace: "open-cluster-management-addon",
					},
				}
				_ = k8sClient.Delete(ctx, proxyCASecret)

				// ManagedCluster with URL
				managedCluster := &clusterv1.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: clusterNS},
					Spec: clusterv1.ManagedClusterSpec{
						ManagedClusterClientConfigs: []clusterv1.ClientConfig{
							{URL: "https://api.cluster1.example.com:6443"},
						},
					},
				}
				Expect(k8sClient.Create(ctx, managedCluster)).To(Succeed())

				tokenSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sveltos-ocm-token",
						Namespace: clusterNS,
					},
					Data: map[string][]byte{
						"token":  []byte("test-token"),
						"ca.crt": []byte("test-ca-cert"),
					},
				}
				Expect(k8sClient.Create(ctx, tokenSecret)).To(Succeed())

				msa := &authv1beta1.ManagedServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      ManagedServiceAccountName,
						Namespace: clusterNS,
					},
				}
				Expect(k8sClient.Create(ctx, msa)).To(Succeed())
				msa.Status.TokenSecretRef = &authv1beta1.SecretRef{
					Name:                 tokenSecret.Name,
					LastRefreshTimestamp: metav1.Now(),
				}
				Expect(k8sClient.Status().Update(ctx, msa)).To(Succeed())

				// Set managed-serviceaccount addon to available
				setAddonAvailable("managed-serviceaccount", clusterNS)

				By("Running reconciliation")
				_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
				result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

				// Reconciliation should succeed but requeue due to missing cluster-proxy cert
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			})
		})

		Describe("Multiple clusters", func() {
			It("should handle multiple ManagedClusterAddOns", func() {
				cluster2NS := "cluster2"
				// Create second cluster namespace
				namespace := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: cluster2NS},
				}
				err := k8sClient.Create(ctx, namespace)
				if err != nil && !apierrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}

				By("Creating test setup with two clusters")
				resource := &sveltosv1alpha1.SveltosOCMCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: testNamespace,
					},
					Spec: sveltosv1alpha1.SveltosOCMClusterSpec{
						LabelSync: true,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				// Create addons for both clusters
				for _, ns := range []string{clusterNS, cluster2NS} {
					addon := &addonv1alpha1.ManagedClusterAddOn{
						ObjectMeta: metav1.ObjectMeta{
							Name:      AddonName,
							Namespace: ns,
						},
					}
					Expect(k8sClient.Create(ctx, addon)).To(Succeed())

					// Set addon to available status
					setAddonAvailable(AddonName, ns)

					mc := &clusterv1.ManagedCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:   ns,
							Labels: map[string]string{"cluster": ns},
						},
						Spec: clusterv1.ManagedClusterSpec{
							ManagedClusterClientConfigs: []clusterv1.ClientConfig{
								{URL: "https://api." + ns + ".example.com:6443"},
							},
						},
					}
					Expect(k8sClient.Create(ctx, mc)).To(Succeed())

					tokenSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sveltos-ocm-token",
							Namespace: ns,
						},
						Data: map[string][]byte{
							"token":  []byte("test-token-" + ns),
							"ca.crt": []byte("test-ca-cert"),
						},
					}
					Expect(k8sClient.Create(ctx, tokenSecret)).To(Succeed())

					msa := &authv1beta1.ManagedServiceAccount{
						ObjectMeta: metav1.ObjectMeta{
							Name:      ManagedServiceAccountName,
							Namespace: ns,
						},
					}
					Expect(k8sClient.Create(ctx, msa)).To(Succeed())
					msa.Status.TokenSecretRef = &authv1beta1.SecretRef{
						Name:                 tokenSecret.Name,
						LastRefreshTimestamp: metav1.Now(),
					}
					Expect(k8sClient.Status().Update(ctx, msa)).To(Succeed())
				}

				By("Running reconciliation")
				_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

				// Set managed-serviceaccount addons to available for both clusters
				for _, ns := range []string{clusterNS, cluster2NS} {
					setAddonAvailable("managed-serviceaccount", ns)
				}

				_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

				By("Verifying both SveltosClusters were created")
				for _, ns := range []string{clusterNS, cluster2NS} {
					sc := &libsveltosv1beta1.SveltosCluster{}
					scKey := types.NamespacedName{Name: ns, Namespace: ns}
					Expect(k8sClient.Get(ctx, scKey, sc)).To(Succeed())
					Expect(sc.Labels).To(HaveKeyWithValue("cluster", ns))
				}
				defer func() {
					addon := &addonv1alpha1.ManagedClusterAddOn{}
					_ = k8sClient.Get(ctx, types.NamespacedName{Name: AddonName, Namespace: cluster2NS}, addon)
					_ = k8sClient.Delete(ctx, addon)

					msa := &authv1beta1.ManagedServiceAccount{}
					_ = k8sClient.Get(ctx, types.NamespacedName{Name: ManagedServiceAccountName, Namespace: cluster2NS}, msa)
					_ = k8sClient.Delete(ctx, msa)

					mc := &clusterv1.ManagedCluster{}
					_ = k8sClient.Get(ctx, types.NamespacedName{Name: cluster2NS}, mc)
					_ = k8sClient.Delete(ctx, mc)

					sc := &libsveltosv1beta1.SveltosCluster{}
					_ = k8sClient.Get(ctx, types.NamespacedName{Name: cluster2NS, Namespace: cluster2NS}, sc)
					_ = k8sClient.Delete(ctx, sc)

					secret := &corev1.Secret{}
					_ = k8sClient.Get(ctx, types.NamespacedName{Name: defaultSveltosKubeconfigName(cluster2NS), Namespace: cluster2NS}, secret)
					_ = k8sClient.Delete(ctx, secret)

					tokenSecret := &corev1.Secret{}
					_ = k8sClient.Get(ctx, types.NamespacedName{Name: "sveltos-ocm-token", Namespace: cluster2NS}, tokenSecret)
					_ = k8sClient.Delete(ctx, tokenSecret)
				}()
			})
		})
	})

	Context("configureManagedServiceAccount", func() {
		It("should configure ManagedServiceAccount with correct spec", func() {
			reconciler := &SveltosOCMClusterReconciler{}

			msa := &authv1beta1.ManagedServiceAccount{}
			sveltosOCMCluster := &sveltosv1alpha1.SveltosOCMCluster{
				Spec: sveltosv1alpha1.SveltosOCMClusterSpec{
					TokenValidity: metav1.Duration{Duration: 24 * time.Hour},
				},
			}

			reconciler.configureManagedServiceAccount(msa, sveltosOCMCluster)

			Expect(msa.Spec.Rotation.Enabled).To(BeTrue())
			Expect(msa.Spec.Rotation.Validity.Duration).To(Equal(24 * time.Hour))
		})
	})
})

var _ = Describe("Constants", func() {
	It("should have correct default values", func() {
		Expect(AddonName).To(Equal("sveltos-ocm-addon"))
		Expect(ManagedServiceAccountName).To(Equal("sveltos-ocm"))
		Expect(FinalizerName).To(Equal("sveltos.open-cluster-management.io/finalizer"))
		Expect(DefaultTokenValidity).To(Equal("168h"))
	})
})
