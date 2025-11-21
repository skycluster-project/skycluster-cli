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

		// Validate flags
		if enable == disable {
			// either both false or both true -> invalid
			log.Fatalf("please specify exactly one of --enable or --disable")
			return
		}
		if enable && name != "" {
			log.Fatalf("-n/--name is only valid when --disable is used")
			return
		}

		ns := ""
		
		if enable {
			if err := enableSSHEntries(ns); err != nil {
				log.Fatalf("error enabling ssh entries: %v", err)
			}
		} else {
			if err := disableSSHEntries(ns, name); err != nil {
				log.Fatalf("error disabling ssh entries: %v", err)
			}
		}
	},
}

// enableSSHEntries will ensure there is an ssh config entry for each xprovider that has a public IP.
// It will create ~/.ssh/config if necessary. Existing entries for the same host name are updated.
func enableSSHEntries(ns string) error {
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("creating dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xproviders",
	}

	resources, err := dynamicClient.Resource(gvr).Namespace(ns).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing xproviders: %w", err)
	}
	if len(resources.Items) == 0 {
		fmt.Printf("No XProviders found in namespace %s\n", ns)
		return nil
	}

	sshConfigPath := getSSHConfigPath()
	lines, err := readSSHConfig(sshConfigPath)
	if err != nil {
		return err
	}

	// For each provider with a public IP ensure or update entry
	updated := false
	for _, res := range resources.Items {
		name := res.GetName()
		stat, found, _ := unstructured.NestedStringMap(res.Object, "status", "gateway")
		if !found {
			continue
		}
		pubIp := ""
		if v, ok := stat["publicIp"]; ok {
			pubIp = v
		}
		if strings.TrimSpace(pubIp) == "" {
			fmt.Printf("skipping provider %s: no public IP\n", name)
			continue
		}

		changed := false
		lines, changed = upsertHostBlock(lines, name, pubIp)
		if changed {
			updated = true
			fmt.Printf("added/updated ssh entry for %s -> %s\n", name, pubIp)
		}
	}

	if updated {
		if err := writeSSHConfig(sshConfigPath, lines); err != nil {
			return fmt.Errorf("writing ssh config: %w", err)
		}
	} else {
		fmt.Println("ssh config is already up-to-date")
	}
	return nil
}

// disableSSHEntries will remove the ssh config entry for a single provider (if name provided)
// or for all providers otherwise.
func disableSSHEntries(ns string, name string) error {
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("creating dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xproviders",
	}

	resources, err := dynamicClient.Resource(gvr).Namespace(ns).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing xproviders: %w", err)
	}

	sshConfigPath := getSSHConfigPath()
	lines, err := readSSHConfig(sshConfigPath)
	if err != nil {
		return err
	}

	if name != "" {
		// Only remove for the provided name
		newLines, removed := removeAllHostEntries(lines, name)
		if !removed {
			fmt.Printf("no ssh entry found for %s\n", name)
			return nil
		}
		if err := writeSSHConfig(sshConfigPath, newLines); err != nil {
			return fmt.Errorf("writing ssh config: %w", err)
		}
		fmt.Printf("removed ssh entry for %s\n", name)
		return nil
	}

	// name == "" -> remove entries for all providers
	// Build a set of provider names to remove
	providerNames := map[string]struct{}{}
	for _, res := range resources.Items {
		providerNames[res.GetName()] = struct{}{}
	}
	if len(providerNames) == 0 {
		fmt.Printf("no xproviders found in namespace %s\n", ns)
		return nil
	}

	newLines := lines
	anyRemoved := false
	for pname := range providerNames {
		var removed bool
		newLines, removed = removeAllHostEntries(newLines, pname)
		if removed {
			anyRemoved = true
			fmt.Printf("removed ssh entry for %s\n", pname)
		}
	}
	if anyRemoved {
		if err := writeSSHConfig(sshConfigPath, newLines); err != nil {
			return fmt.Errorf("writing ssh config: %w", err)
		}
	} else {
		fmt.Println("no ssh entries found for any providers")
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
	return filepath.Join(home, ".ssh", "config")
}

func readSSHConfig(path string) ([]string, error) {
	// If file does not exist, return empty lines (we will create it later)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading ssh config %s: %w", path, err)
	}
	// split by lines, preserve as-is except strip trailing CR
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning ssh config: %w", err)
	}
	return lines, nil
}

func writeSSHConfig(path string, lines []string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating .ssh dir: %w", err)
	}
	// Join lines with newline and ensure trailing newline
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	// Write file with 0600 permission
	if err := os.WriteFile(path, []byte(out), 0600); err != nil {
		return fmt.Errorf("writing ssh config: %w", err)
	}
	return nil
}

// upsertHostBlock ensures there is exactly one Host block for the given host name and
// that the block sets HostName to the provided ip and User ubuntu.
// Returns updated lines and whether a change occurred.
func upsertHostBlock(lines []string, host string, ip string) ([]string, bool) {
	// Remove all existing host blocks for `host` first to avoid duplicates.
	cleaned, removedAny := removeAllHostEntries(lines, host)

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
		}
	}
	return cleaned, changed
}

func hostBlockMatchesAtEnd(lines []string, block []string) bool {
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
		return false
	}
	for i := 0; i < len(block); i++ {
		if strings.TrimRight(lines[start+i], "\r\n") != block[i] {
			return false
		}
	}
	return true
}

// removeAllHostEntries removes all Host blocks that include the host token in their Host line.
// Returns the new lines and whether any removal occurred.
func removeAllHostEntries(lines []string, host string) ([]string, bool) {
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
	return out, removed
}