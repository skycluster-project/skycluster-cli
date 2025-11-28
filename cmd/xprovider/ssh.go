package xprovider

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/etesami/skycluster-cli/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	// ssh command flags
	xProviderSSHCmd.PersistentFlags().Bool("enable", false, "Enable SSH entries for all XProviders")
	xProviderSSHCmd.PersistentFlags().Bool("disable", false, "Disable SSH entries for XProviders")
	xProviderSSHCmd.PersistentFlags().StringP("name", "n", "", "Name of the XProvider (used only with --disable)")

	// Note: hook-up of xProviderSSHCmd into the parent command tree should be done
	// where commands are assembled (not shown here).
}

var xProviderSSHCmd = &cobra.Command{
	Use:   "ssh",
	Short: "Manage ~/.ssh/config entries for XProviders",
	Run: func(cmd *cobra.Command, args []string) {
		enable, _ := cmd.Flags().GetBool("enable")
		disable, _ := cmd.Flags().GetBool("disable")
		name, _ := cmd.Flags().GetString("name")

		debugf("ssh command invoked: enable=%v disable=%v name=%q", enable, disable, name)

		// Validate flags
		if enable == disable {
			// either both false or both true -> invalid
			debugf("invalid flags: enable == disable (%v)", enable)
			log.Fatalf("please specify exactly one of --enable or --disable")
			return
		}
		if enable && name != "" {
			debugf("invalid flags: --name provided with --enable")
			log.Fatalf("-n/--name is only valid when --disable is used")
			return
		}

		ns := ""

		if enable {
			debugf("calling enableSSHEntries for namespace %q", ns)
			if err := enableSSHEntries(ns); err != nil {
				debugf("enableSSHEntries returned error: %v", err)
				log.Fatalf("error enabling ssh entries: %v", err)
			}
		} else {
			debugf("calling disableSSHEntries for namespace %q name=%q", ns, name)
			if err := disableSSHEntries(ns, name); err != nil {
				debugf("disableSSHEntries returned error: %v", err)
				log.Fatalf("error disabling ssh entries: %v", err)
			}
		}
	},
}

// enableSSHEntries will ensure there is an ssh config entry for each xprovider that has a public IP.
// It will create ~/.ssh/config if necessary. Existing entries for the same host name are updated.
func enableSSHEntries(ns string) error {
	kubeconfig := viper.GetString("kubeconfig")
	debugf("enableSSHEntries: kubeconfig=%q namespace=%q", kubeconfig, ns)
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		debugf("failed creating dynamic client: %v", err)
		return fmt.Errorf("creating dynamic client: %w", err)
	}
	debugf("dynamic client initialized")

	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xproviders",
	}

	debugf("listing xproviders in namespace %q", ns)
	resources, err := dynamicClient.Resource(gvr).Namespace(ns).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		debugf("listing xproviders failed: %v", err)
		return fmt.Errorf("listing xproviders: %w", err)
	}
	debugf("found %d xproviders", len(resources.Items))
	if len(resources.Items) == 0 {
		fmt.Printf("No XProviders found in namespace %s\n", ns)
		return nil
	}

	sshConfigPath := getSSHConfigPath()
	debugf("ssh config path: %s", sshConfigPath)
	lines, err := readSSHConfig(sshConfigPath)
	if err != nil {
		debugf("readSSHConfig failed: %v", err)
		return err
	}
	debugf("read %d lines from ssh config", len(lines))

	// For each provider with a public IP ensure or update entry
	updated := false
	for _, res := range resources.Items {
		name := res.GetName()
		stat, found, _ := unstructured.NestedStringMap(res.Object, "status", "gateway")
		if !found {
			debugf("provider %s: status.gateway not found, skipping", name)
			continue
		}
		pubIp := ""
		if v, ok := stat["publicIp"]; ok {
			pubIp = v
		}
		if strings.TrimSpace(pubIp) == "" {
			fmt.Printf("skipping provider %s: no public IP\n", name)
			debugf("provider %s has empty publicIp, skipping", name)
			continue
		}

		debugf("ensuring ssh entry for provider %s -> %s", name, pubIp)
		changedLines, changed := upsertHostBlock(lines, name, pubIp)
		if changed {
			updated = true
			lines = changedLines
			fmt.Printf("added/updated ssh entry for %s -> %s\n", name, pubIp)
			debugf("ssh entry updated for %s", name)
		} else {
			debugf("no change needed for %s", name)
		}
	}

	if updated {
		debugf("writing updated ssh config to %s", sshConfigPath)
		if err := writeSSHConfig(sshConfigPath, lines); err != nil {
			debugf("writeSSHConfig failed: %v", err)
			return fmt.Errorf("writing ssh config: %w", err)
		}
		debugf("wrote ssh config successfully")
	} else {
		fmt.Println("ssh config is already up-to-date")
		debugf("no updates required to ssh config")
	}
	return nil
}

// disableSSHEntries will remove the ssh config entry for a single provider (if name provided)
// or for all providers otherwise.
func disableSSHEntries(ns string, name string) error {
	kubeconfig := viper.GetString("kubeconfig")
	debugf("disableSSHEntries: kubeconfig=%q namespace=%q name=%q", kubeconfig, ns, name)
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		debugf("failed creating dynamic client: %v", err)
		return fmt.Errorf("creating dynamic client: %w", err)
	}
	debugf("dynamic client initialized")

	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xproviders",
	}

	debugf("listing xproviders in namespace %q", ns)
	resources, err := dynamicClient.Resource(gvr).Namespace(ns).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		debugf("listing xproviders failed: %v", err)
		return fmt.Errorf("listing xproviders: %w", err)
	}
	debugf("found %d xproviders", len(resources.Items))

	sshConfigPath := getSSHConfigPath()
	debugf("ssh config path: %s", sshConfigPath)
	lines, err := readSSHConfig(sshConfigPath)
	if err != nil {
		debugf("readSSHConfig failed: %v", err)
		return err
	}
	debugf("read %d lines from ssh config", len(lines))

	if name != "" {
		debugf("removing entries for provider %s only", name)
		// Only remove for the provided name
		newLines, removed := removeAllHostEntries(lines, name)
		if !removed {
			fmt.Printf("no ssh entry found for %s\n", name)
			debugf("no entries removed for %s", name)
			return nil
		}
		if err := writeSSHConfig(sshConfigPath, newLines); err != nil {
			debugf("writeSSHConfig failed: %v", err)
			return fmt.Errorf("writing ssh config: %w", err)
		}
		fmt.Printf("removed ssh entry for %s\n", name)
		debugf("removed entries for %s and wrote file", name)
		return nil
	}

	debugf("removing entries for all providers")
	// name == "" -> remove entries for all providers
	// Build a set of provider names to remove
	providerNames := map[string]struct{}{}
	for _, res := range resources.Items {
		providerNames[res.GetName()] = struct{}{}
	}
	if len(providerNames) == 0 {
		fmt.Printf("no xproviders found in namespace %s\n", ns)
		debugf("no providers found to remove entries for")
		return nil
	}

	newLines := lines
	anyRemoved := false
	for pname := range providerNames {
		debugf("attempting to remove entries for provider %s", pname)
		var removed bool
		newLines, removed = removeAllHostEntries(newLines, pname)
		if removed {
			anyRemoved = true
			fmt.Printf("removed ssh entry for %s\n", pname)
			debugf("removed entries for %s", pname)
		} else {
			debugf("no ssh entry found for %s", pname)
		}
	}
	if anyRemoved {
		debugf("writing updated ssh config to %s", sshConfigPath)
		if err := writeSSHConfig(sshConfigPath, newLines); err != nil {
			debugf("writeSSHConfig failed: %v", err)
			return fmt.Errorf("writing ssh config: %w", err)
		}
		debugf("wrote ssh config successfully")
	} else {
		fmt.Println("no ssh entries found for any providers")
		debugf("no provider entries were removed")
	}
	return nil
}

// Helpers for ssh config manipulation

func getSSHConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// fallback to env var
		home = os.Getenv("HOME")
	}
	path := filepath.Join(home, ".ssh", "config")
	debugf("getSSHConfigPath: %s", path)
	return path
}

func readSSHConfig(path string) ([]string, error) {
	debugf("readSSHConfig path=%s", path)
	// If file does not exist, return empty lines (we will create it later)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		debugf("ssh config does not exist at %s; returning empty slice", path)
		return []string{}, nil
	}
	if err != nil {
		debugf("error reading ssh config %s: %v", path, err)
		return nil, fmt.Errorf("reading ssh config %s: %w", path, err)
	}
	// split by lines, preserve as-is except strip trailing CR
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		debugf("scanner error reading ssh config %s: %v", path, err)
		return nil, fmt.Errorf("scanning ssh config: %w", err)
	}
	debugf("readSSHConfig returned %d lines", len(lines))
	return lines, nil
}

func writeSSHConfig(path string, lines []string) error {
	debugf("writeSSHConfig path=%s lines=%d", path, len(lines))
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		debugf("creating .ssh dir %s failed: %v", dir, err)
		return fmt.Errorf("creating .ssh dir: %w", err)
	}
	// Join lines with newline and ensure trailing newline
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	// Write file with 0600 permission
	if err := os.WriteFile(path, []byte(out), 0600); err != nil {
		debugf("writing ssh config %s failed: %v", path, err)
		return fmt.Errorf("writing ssh config: %w", err)
	}
	debugf("wrote ssh config %s (bytes=%d)", path, len(out))
	return nil
}

// upsertHostBlock ensures there is exactly one Host block for the given host name and
// that the block sets HostName to the provided ip and User ubuntu.
// Returns updated lines and whether a change occurred.
func upsertHostBlock(lines []string, host string, ip string) ([]string, bool) {
	debugf("upsertHostBlock host=%s ip=%s", host, ip)
	// Remove all existing host blocks for `host` first to avoid duplicates.
	cleaned, removedAny := removeAllHostEntries(lines, host)
	debugf("removed existing entries=%v", removedAny)

	// Create the canonical block
	block := []string{
		fmt.Sprintf("Host %s", host),
		fmt.Sprintf("\tHostName %s", ip),
		"\tUser ubuntu",
		"\tStrictHostKeyChecking no",
		"\tUserKnownHostsFile /dev/null",
	}

	// Append a blank line before the block if the file is non-empty and does not already end with a blank line
	if len(cleaned) > 0 && strings.TrimSpace(cleaned[len(cleaned)-1]) != "" {
		cleaned = append(cleaned, "")
	}
	cleaned = append(cleaned, block...)

	// Determine if change occurred: if we removed existing or the resulting block isn't already present
	changed := removedAny
	if !removedAny {
		// Check if an identical block already exists at EOF (most common case)
		if !hostBlockMatchesAtEnd(lines, block) {
			changed = true
			debugf("block not found at end; marking as changed")
		} else {
			debugf("identical block already present at end; no change")
		}
	} else {
		debugf("existing entries removed; change=true")
	}
	return cleaned, changed
}

func hostBlockMatchesAtEnd(lines []string, block []string) bool {
	debugf("hostBlockMatchesAtEnd blockLines=%d fileLines=%d", len(block), len(lines))
	// Compare block to the tail of lines (allowing preceding blank)
	// find start position
	if len(block) == 0 {
		return false
	}
	// skip trailing blank lines
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start := end - len(block)
	if start < 0 {
		debugf("block longer than file tail; no match")
		return false
	}
	for i := 0; i < len(block); i++ {
		if strings.TrimRight(lines[start+i], "\r\n") != block[i] {
			debugf("mismatch at line %d: file=%q block=%q", i, lines[start+i], block[i])
			return false
		}
	}
	debugf("block matches at end")
	return true
}

// removeAllHostEntries removes all Host blocks that include the host token in their Host line.
// Returns the new lines and whether any removal occurred.
func removeAllHostEntries(lines []string, host string) ([]string, bool) {
	debugf("removeAllHostEntries host=%s fileLines=%d", host, len(lines))
	var out []string
	i := 0
	removed := false
	for i < len(lines) {
		line := lines[i]
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "Host ") {
			// tokens after "Host"
			parts := strings.Fields(trim)
			found := false
			for _, tok := range parts[1:] {
				if tok == host {
					found = true
					break
				}
			}
			if found {
				debugf("found Host block for %s at line %d; removing", host, i)
				// skip this block: consume until next Host or EOF
				removed = true
				j := i + 1
				for j < len(lines) {
					if strings.HasPrefix(strings.TrimSpace(lines[j]), "Host ") {
						break
					}
					j++
				}
				i = j
				// also trim trailing blank lines from out if there are multiple blank lines
				for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
					out = out[:len(out)-1]
				}
				// continue without appending this Host block
				continue
			}
		}
		out = append(out, line)
		i++
	}
	debugf("removeAllHostEntries finished removed=%v newLines=%d", removed, len(out))
	return out, removed
}