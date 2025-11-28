package xkube

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	"github.com/etesami/skycluster-cli/internal/utils"
)

// Controller encapsulates state and logic for propagating secrets
// from source xkube clusters to other ready xkubes.
type Controller struct {
	cs     *kubernetes.Clientset
	dyn    dynamic.Interface
	ns     string

	secretLabelSelector string // e.g. "skycluster.io/secret-type=cluster-cacert"
	remoteSecretKey     string // e.g. "remote-secret.yaml"

	// readyXkubes maps clusterName -> kubeconfig
	readyMu sync.Mutex
	ready   map[string]string

	// deployedTracks[source][target] == true when secret from source has been applied to target.
	deployedMu sync.Mutex
	deployed   map[string]map[string]bool

	// for constructing fetchKubeconfig call (matches your original)
	clientSets clientSets
}

// NewController creates and initializes a Controller.
// kubeconfigPath is used to create clientset/dynamic client for the management cluster.
// ns is the namespace where secrets are watched/listed.
func NewController(kubeconfigPath, ns string) (*Controller, error) {
	debugf("NewController: kubeconfig=%q ns=%q", kubeconfigPath, ns)
	cs, err1 := utils.GetClientset(kubeconfigPath)
	dyn, err2 := utils.GetDynamicClient(kubeconfigPath)
	if err1 != nil || err2 != nil {
		// prefer returning first non-nil error
		if err1 != nil {
			debugf("GetClientset failed: %v", err1)
			return nil, fmt.Errorf("creating kubernetes clientset: %w", err1)
		}
		debugf("GetDynamicClient failed: %v", err2)
		return nil, fmt.Errorf("creating dynamic client: %w", err2)
	}

	c := &Controller{
		cs:                  cs,
		dyn:                 dyn,
		ns:                  ns,
		secretLabelSelector: "skycluster.io/secret-type=cluster-cacert",
		remoteSecretKey:     "remote-secret.yaml",
		ready:               make(map[string]string),
		deployed:            make(map[string]map[string]bool),
		clientSets: clientSets{
			dynamicClient: dyn,
			clientSet:     cs,
		},
	}
	debugf("NewController initialized successfully")
	return c, nil
}

// Run starts watchers and blocks until ctx is cancelled. It returns when the context is done.
func (c *Controller) Run(ctx context.Context) error {
	debugf("Controller.Run starting (ns=%q)", c.ns)
	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xkubes",
	}

	// create cancellable child context so we can stop early
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// get initial list to populate counts/map
	list, err := c.dyn.Resource(gvr).List(childCtx, metav1.ListOptions{})
	if err != nil {
		debugf("listing xkubes failed: %v", err)
		return fmt.Errorf("listing xkubemeshes: %w", err)
	}
	debugf("initial xkubes list count=%d", len(list.Items))

	mu := &sync.Mutex{}
	readyMap := make(map[string]bool)
	total, ready := len(list.Items), 0

	// Watch xkubes
	xkubeWatcher, err := c.dyn.Resource(gvr).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		debugf("watch creation failed: %v", err)
		return fmt.Errorf("watching xkubemeshes: %w", err)
	}
	defer xkubeWatcher.Stop()
	debugf("watcher established for xkubes")

	// Event loop goroutines
	var wg sync.WaitGroup
	stopCh := make(chan struct{})
	wg.Add(1)

	// xkube events
	go func() {
		defer wg.Done()
		ch := xkubeWatcher.ResultChan()
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					debugf("watch result channel closed")
					return
				}
				if ev.Object == nil {
					debugf("watch event with nil object received; skipping")
					continue
				}

				obj, ok := ev.Object.(*unstructured.Unstructured)
				if !ok {
					log.Printf("unexpected type from xkube watch: %T", ev.Object)
					continue
				}

				// Determine ready status once
				isReady := utils.GetConditionStatus(obj, "Ready") == "True"
				debugf("event for %s/%s ready=%v", obj.GetNamespace(), obj.GetName(), isReady)

				// update ready map and counts
				key := obj.GetNamespace() + "/" + obj.GetName()
				mu.Lock()
				prev, exists := readyMap[key]
				if !exists { // new entry
					readyMap[key] = isReady
					if isReady {
						ready++
					}
					debugf("new xkube entry %s ready=%v (readyCount=%d total=%d)", key, isReady, ready, total)
				} else { // existing entry
					if prev != isReady {
						if isReady {
							ready++
						} else {
							ready--
						}
						readyMap[key] = isReady
						debugf("updated xkube entry %s prevReady=%v nowReady=%v (readyCount=%d)", key, prev, isReady, ready)
					}
				}

				// If the object is Ready, call the handler
				if isReady {
					debugf("calling handleReadyXkube for %s", key)
					c.handleReadyXkube(obj)
				}

				// stop when all are ready (and there is at least one)
				if total > 0 && ready == total {
					debugf("all xkubes ready (ready=%d total=%d) - cancelling child context", ready, total)
					mu.Unlock()
					cancel() // stops watchers and main wait
					return
				}
				mu.Unlock()

			case <-stopCh:
				debugf("stopCh received - terminating watch goroutine")
				return
			}
		}
	}()

	// Block until context cancelled
	<-childCtx.Done()
	debugf("childCtx done; shutting down")
	close(stopCh)
	wg.Wait()
	debugf("Run completed")
	return nil
}

// handleReadyXkube is called when an xkubemesh shows Ready=true.
// It fetches its kubeconfig, stores it in ready map, and applies existing secrets to it.
func (c *Controller) handleReadyXkube(obj *unstructured.Unstructured) {
	targetClusterName := c.getClusterNameFromXkube(obj)
	log.Printf("handling ready xkube: cluster=%s name=%s", targetClusterName, obj.GetName())
	debugf("handleReadyXkube: obj=%s/%s clusterName=%q", obj.GetNamespace(), obj.GetName(), targetClusterName)
	if targetClusterName == "" {
		debugf("no clusterName found for xkube %s/%s - skipping", obj.GetNamespace(), obj.GetName())
		return // cannot proceed without cluster name
	}

	// fetch kubeconfig for this xkube (assumes fetchKubeconfig exists in your codebase)
	kc, err := fetchKubeconfig(obj.GetName(), c.clientSets)
	if err != nil || strings.TrimSpace(kc) == "" {
		log.Printf("warning: kubeconfig for mesh %s is empty or fetch failed; will retry later: err=%v", obj.GetName(), err)
		debugf("fetchKubeconfig failed or returned empty for %s: err=%v", obj.GetName(), err)
		return
	}
	debugf("fetched kubeconfig for xkube %s (len=%d)", obj.GetName(), len(kc))

	c.setReady(targetClusterName, kc)
	debugf("setReady for cluster %s", targetClusterName)
	log.Printf("xkube ready: cluster=%s name=%s", targetClusterName, obj.GetName())

	// apply all existing relevant secrets into this target (except those from the same source)
	secrets, err := c.listSecrets(context.Background())
	if err != nil {
		log.Printf("error listing secrets for propagation to %s: %v", targetClusterName, err)
		debugf("listSecrets failed: %v", err)
		return
	}
	debugf("listSecrets returned %d secrets", len(secrets))

	for i := range secrets {
		secret := secrets[i] // avoid pointer to loop var
		sourceClusterName := secret.Labels["skycluster.io/cluster-name"]
		if sourceClusterName == "" || sourceClusterName == targetClusterName {
			debugf("skipping secret %s/%s source=%q target=%q", secret.Namespace, secret.Name, sourceClusterName, targetClusterName)
			continue
		}

		if c.isDeployed(sourceClusterName, targetClusterName) {
			debugf("secret from source=%s already deployed to target=%s - skipping", sourceClusterName, targetClusterName)
			continue
		}

		debugf("applying secret %s/%s from %s to target=%s", secret.Namespace, secret.Name, sourceClusterName, targetClusterName)
		if err := c.applySecretToRemote(context.Background(), kc, &secret); err != nil {
			log.Printf("error applying secret %s/%s from %s to %s: %v", secret.Namespace, secret.Name, sourceClusterName, targetClusterName, err)
			debugf("applySecretToRemote failed: %v", err)
			continue
		}
		c.markDeployed(sourceClusterName, targetClusterName)
		debugf("marked deployed source=%s target=%s", sourceClusterName, targetClusterName)
		log.Printf("propagated secret (source=%s) to target=%s", sourceClusterName, targetClusterName)
	}
}

// applySecretToRemote creates or updates the given secret on the remote cluster described by kubeconfig (kc).
// It applies the secret into the same namespace and name as originSecret.
func (c *Controller) applySecretToRemote(ctx context.Context, kc string, originSecret *corev1.Secret) error {
	debugf("applySecretToRemote: origin=%s/%s targetKubeconfigLen=%d", originSecret.Namespace, originSecret.Name, len(kc))
	if strings.TrimSpace(kc) == "" {
		debugf("empty kubeconfig provided")
		return fmt.Errorf("empty kubeconfig for target cluster")
	}

	// Get embedded YAML from origin secret
	raw, ok := originSecret.Data[c.remoteSecretKey]
	if !ok || len(raw) == 0 {
		debugf("origin secret missing embedded key %q", c.remoteSecretKey)
		return fmt.Errorf("secret %s/%s missing key %q", originSecret.Namespace, originSecret.Name, c.remoteSecretKey)
	}

	// Unmarshal YAML into a corev1.Secret
	var remoteSecret corev1.Secret
	if err := yaml.Unmarshal(raw, &remoteSecret); err != nil {
		debugf("unmarshal embedded secret YAML failed: %v", err)
		return fmt.Errorf("failed to unmarshal embedded secret YAML from %s/%s: %w", originSecret.Namespace, originSecret.Name, err)
	}
	debugf("unmarshalled embedded secret YAML: name=%q namespace=%q", remoteSecret.Name, remoteSecret.Namespace)

	// Ensure name and namespace are present
	name := remoteSecret.Name
	namespace := remoteSecret.Namespace
	if name == "" || namespace == "" {
		debugf("embedded secret missing name/namespace")
		return fmt.Errorf("embedded secret YAML must include metadata.name and metadata.namespace (from %s/%s)", originSecret.Namespace, originSecret.Name)
	}

	// Build rest.Config and remote typed client
	remoteClient, err := utils.GetClientsetFromString(kc)
	if err != nil {
		debugf("GetClientsetFromString failed: %v", err)
		return fmt.Errorf("creating remote clientset: %w", err)
	}
	debugf("remote clientset created for target cluster")

	// short timeout for remote operation
	ctx2, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	// Try to get existing secret on remote cluster
	existing, err := remoteClient.CoreV1().Secrets(namespace).Get(ctx2, name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			debugf("remote secret %s/%s not found - creating", namespace, name)
			// Create
			_, err = remoteClient.CoreV1().Secrets(namespace).Create(ctx2, &remoteSecret, metav1.CreateOptions{})
			if err != nil {
				debugf("creating remote secret failed: %v", err)
				return fmt.Errorf("creating secret %s/%s on remote cluster: %w", namespace, name, err)
			}
			debugf("created secret %s/%s on remote", namespace, name)
			return nil
		}
		debugf("getting remote secret failed: %v", err)
		return fmt.Errorf("getting remote secret %s/%s: %w", namespace, name, err)
	}

	// Exists -> update. Preserve resourceVersion for optimistic concurrency.
	remoteSecret.ResourceVersion = existing.ResourceVersion
	debugf("updating existing remote secret %s/%s (resourceVersion=%s)", namespace, name, remoteSecret.ResourceVersion)
	_, err = remoteClient.CoreV1().Secrets(namespace).Update(ctx2, &remoteSecret, metav1.UpdateOptions{})
	if err != nil {
		debugf("updating remote secret failed: %v", err)
		return fmt.Errorf("updating secret %s/%s on remote cluster: %w", namespace, name, err)
	}
	debugf("updated remote secret %s/%s successfully", namespace, name)
	return nil
}

// listSecrets returns secrets in controller namespace that match the label selector.
func (c *Controller) listSecrets(ctx context.Context) ([]corev1.Secret, error) {
	debugf("listSecrets: ns=%q selector=%q", c.ns, c.secretLabelSelector)
	opts := metav1.ListOptions{LabelSelector: c.secretLabelSelector}
	ls, err := c.cs.CoreV1().Secrets(c.ns).List(ctx, opts)
	if err != nil {
		debugf("list secrets failed: %v", err)
		return nil, err
	}
	debugf("listSecrets returned %d items", len(ls.Items))
	return ls.Items, nil
}

// getClusterNameFromXkube extracts the clusterName from xkubemesh unstructured object,
// trying status.clusterName as string or slice, falling back to resource name externally.
func (c *Controller) getClusterNameFromXkube(obj *unstructured.Unstructured) string {
	if s, found, _ := unstructured.NestedString(obj.Object, "status", "clusterName"); found && s != "" {
		debugf("getClusterNameFromXkube: found status.clusterName=%q for %s/%s", s, obj.GetNamespace(), obj.GetName())
		return s
	}
	debugf("getClusterNameFromXkube: no status.clusterName for %s/%s", obj.GetNamespace(), obj.GetName())
	return ""
}

// --- deployed bookkeeping helpers ---
func (c *Controller) markDeployed(source, target string) {
	debugf("markDeployed: source=%s target=%s", source, target)
	c.deployedMu.Lock()
	defer c.deployedMu.Unlock()
	if _, ok := c.deployed[source]; !ok {
		c.deployed[source] = make(map[string]bool)
	}
	c.deployed[source][target] = true
}

func (c *Controller) isDeployed(source, target string) bool {
	c.deployedMu.Lock()
	defer c.deployedMu.Unlock()
	if m, ok := c.deployed[source]; ok {
		debugf("isDeployed: source=%s target=%s -> %v", source, target, m[target])
		return m[target]
	}
	debugf("isDeployed: no entries for source=%s", source)
	return false
}

func (c *Controller) clearDeployedForSource(source string) {
	debugf("clearDeployedForSource: source=%s", source)
	c.deployedMu.Lock()
	defer c.deployedMu.Unlock()
	delete(c.deployed, source)
}

// ready map helpers
func (c *Controller) setReady(clusterName, kc string) {
	debugf("setReady: cluster=%s", clusterName)
	c.readyMu.Lock()
	defer c.readyMu.Unlock()
	c.ready[clusterName] = kc
}

func (c *Controller) unsetReady(clusterName string) {
	debugf("unsetReady: cluster=%s", clusterName)
	c.readyMu.Lock()
	defer c.readyMu.Unlock()
	delete(c.ready, clusterName)
}