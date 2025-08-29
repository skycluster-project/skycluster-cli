package image

import (
	"context"
	"fmt"
	"log"
	"maps"
	"os"
	"strings"
	"text/tabwriter"

	vars "github.com/etesami/skycluster-cli/internal"
	utils "github.com/etesami/skycluster-cli/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var pNames []string

func init() {
	imageCmd.AddCommand(imageListCmd)
	imageListCmd.PersistentFlags().StringSliceVarP(&pNames, "provider-name", "p", nil, "Provider Names, seperated by comma")
}

var imageCmd = &cobra.Command{
	Use:   "image commands",
	Short: "Image commands",
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

var imageListCmd = &cobra.Command{
	Use:   "list",
	Short: "List avaialble images across providers",
	Run: func(cmd *cobra.Command, args []string) {
		listImages()
	},
}

func listImages() {
	kconfig := viper.GetStringMapString("kubeconfig")
	kubeconfig := kconfig["sky-manager"]
	clientset, err := utils.GetClientset(kubeconfig)
	if err != nil {
		log.Fatalf("Error getting clientset: %v", err)
		return
	}
	imageList := make(map[string][]string, 0)
	baseFilters := "skycluster.io/managed-by=skycluster, skycluster.io/config-type=provider-mappings"
	for _, n := range pNames {
		filters := baseFilters + ", skycluster.io/provider-name=" + n
		filteredImages := getImageData(clientset, filters)
		maps.Copy(imageList, filteredImages)
	}

	// no provider names provided, get all images
	if len(pNames) == 0 {
		filteredImages := getImageData(clientset, baseFilters)
		maps.Copy(imageList, filteredImages)
	}
	availableImages := utils.IntersectionOfMapValues(imageList, utils.GetMapStringKeys(imageList))
	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	if len(availableImages) == 0 {
		fmt.Println("No images available")
	} else {
		fmt.Fprintln(writer, "NAME\tOFFERED BY")
	}
	for _, f := range availableImages {
		fmt.Fprintf(writer, "%s\t%d\n", f, len(imageList))
	}
	writer.Flush()
}

func getImageData(clientset *kubernetes.Clientset, filters string) map[string][]string {
	imageList := make(map[string][]string, 0)
	confgis, err := clientset.CoreV1().ConfigMaps(vars.SkyClusterName).List(context.Background(), metav1.ListOptions{
		LabelSelector: filters,
	})
	if err != nil {
		log.Fatalf("Error listing configmaps: %v", err)
	}

	for _, cm := range confgis.Items {
		fList := make([]string, 0)
		pName := cm.Labels["skycluster.io/provider-name"]
		pRegion := cm.Labels["skycluster.io/provider-region"]
		pZone := cm.Labels["skycluster.io/provider-zone"]
		pID := pName + "_" + pRegion + "_" + pZone
		for d, _ := range cm.Data {
			if strings.Contains(d, "image") {
				fList = append(fList, d)
			}
		}
		if len(fList) > 0 {
			imageList[pID] = fList
		}
	}
	return imageList
}

func GetImageCmd() *cobra.Command {
	return imageCmd
}
