package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	tfFS "github.com/hashicorp/hc-install/fs"
	tfProduct "github.com/hashicorp/hc-install/product"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/r3labs/diff/v3"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/zclconf/go-cty/cty"
)

func lxdClient(
	clientCrtFile string,
	clientKeyFile string,
	clusterAddr string,
) (lxd.InstanceServer, error) {
	certPEMBlock, err := os.ReadFile(clientCrtFile)
	if err != nil {
		return nil, err
	}

	keyPEMBlock, err := os.ReadFile(clientKeyFile)
	if err != nil {
		return nil, err
	}

	client, err := lxd.ConnectLXD(clusterAddr, &lxd.ConnectionArgs{
		TLSClientCert:      string(certPEMBlock),
		TLSClientKey:       string(keyPEMBlock),
		InsecureSkipVerify: true,
		SkipGetServer:      true,
	})
	if err != nil {
		return nil, err
	}

	return client, nil
}

func getInventory(
	clusterCert string,
	clusterKey string,
	clusterAddr string,
	l *Logger,
) (inventory []*HCLResourceNode, err error) {
	c, err := lxdClient(
		clusterCert,
		clusterKey,
		clusterAddr,
	)
	if err != nil {
		return nil, err
	}

	srcGraph, err := buildResourcesInventory(c, l)
	if err != nil {
		return nil, fmt.Errorf("Error building resources inventory: %w", err)
	}

	inventory, err = srcGraph.HCLOrder(l)
	if err != nil {
		return nil, fmt.Errorf("Error ordering resources inventory: %w", err)
	}

	return inventory, nil
}

func getInventories(
	srcClusterCert string,
	srcClusterKey string,
	srcClusterAddr string,
	dstClusterCert string,
	dstClusterKey string,
	dstClusterAddr string,
	verbose bool,
) (srcInventory []*HCLResourceNode, dstInventory []*HCLResourceNode, err error) {
	srcClient, err := lxdClient(
		srcClusterCert,
		srcClusterKey,
		srcClusterAddr,
	)
	if err != nil {
		return nil, nil, err
	}

	dstClient, err := lxdClient(
		dstClusterCert,
		dstClusterKey,
		dstClusterAddr,
	)
	if err != nil {
		return nil, nil, err
	}

	errors := make([]error, 0)
	errs := make(chan error, 2)
	wg := &sync.WaitGroup{}
	wg.Add(2)

	go func() {
		wg.Wait()
		close(errs)
	}()

	go func() {
		defer wg.Done()
		l := NewLogger(verbose, "sync/src_inventory_building")
		srcGraph, err := buildResourcesInventory(srcClient, l)
		if err != nil {
			errs <- fmt.Errorf("Error building source resources inventory: %w", err)
		}

		srcInventory, err = srcGraph.HCLOrder(l)
		if err != nil {
			errs <- fmt.Errorf("Error ordering source resources inventory: %w", err)
		}
	}()

	go func() {
		defer wg.Done()
		l := NewLogger(verbose, "sync/dst_inventory_building")
		dstGraph, err := buildResourcesInventory(dstClient, l)
		if err != nil {
			errs <- fmt.Errorf("Error building destination resources inventory: %w", err)
		}

		dstInventory, err = dstGraph.HCLOrder(l)
		if err != nil {
			errs <- fmt.Errorf("Error ordering destination resources inventory: %w", err)
		}
	}()

	for err := range errs {
		if err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		formattedErrors := ""
		for i, err := range errors {
			formattedErrors += fmt.Sprintf("Error %d: %s\n", i, err)
		}

		return nil, nil, fmt.Errorf("Error building resources inventory:\n%s", formattedErrors)
	}

	return srcInventory, dstInventory, nil
}

func rootHCLDefFile() (hclDef *hclwrite.File, rootBody *hclwrite.Body, providerBody *hclwrite.Body) {
	hclDef = hclwrite.NewEmptyFile()
	rootBody = hclDef.Body()
	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()
	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()
	requiredProvidersBody.SetAttributeValue("lxd", cty.ObjectVal(map[string]cty.Value{"source": cty.StringVal("terraform-lxd/lxd")}))
	rootBody.AppendNewline()
	providerBlock := rootBody.AppendNewBlock("provider", []string{"lxd"})
	providerBody = providerBlock.Body()
	providerBody.SetAttributeValue("accept_remote_certificate", cty.BoolVal(true))
	rootBody.AppendNewline()

	return hclDef, rootBody, providerBody
}

func addTFResource(rootBody *hclwrite.Body, node *HCLResourceNode, dstRemoteName string, extraResAttr map[string]cty.Value, l *Logger) error {
	switch node.Label {
	case "lxd_project", "lxd_network_zone":
		resourceBlock := rootBody.AppendNewBlock("resource", []string{node.Label, node.ID})
		resourceBody := resourceBlock.Body()
		// Thanks to hcl struct tags, we can use gohcl to encode most of the fields of the resource.
		// Some fields might need to be set manually (e.g device block, if a resource is present in all target, we must add multiple resource blocks, etc).
		gohcl.EncodeIntoBody(node.Data, resourceBody)
		resourceBody.AppendNewline()
		resourceBody.SetAttributeValue("remote", cty.StringVal(dstRemoteName))

		l.Info(fmt.Sprintf("Adding resource %q with ID %q to HCL definition", node.Label, node.ID), nil)
		for k, v := range extraResAttr {
			resourceBody.SetAttributeValue(k, v)
			l.Info(fmt.Sprintf("Adding extra attribute %q to resource %q", k, node.Label), nil)
		}

		rootBody.AppendNewline()
	case "lxd_network_zone_record":
		resourceBlock := rootBody.AppendNewBlock("resource", []string{node.Label, node.ID})
		resourceBody := resourceBlock.Body()
		gohcl.EncodeIntoBody(node.Data, resourceBody)
		resourceBody.SetAttributeValue("remote", cty.StringVal(dstRemoteName))
		l.Info(fmt.Sprintf("Adding resource %q with ID %q to HCL definition", node.Label, node.ID), nil)

		// Add the 'zone' and 'project' fields to the resource body.
		for _, dep := range node.DependsOn {
			if dep.Label == "lxd_network_zone" {
				rawExpr := fmt.Sprintf("lxd_network_zone.%s.name", dep.ID)
				tr, diag := hclsyntax.ParseTraversalAbs([]byte(rawExpr), "", hcl.Pos{Line: 1, Column: 1})
				if diag.HasErrors() {
					return fmt.Errorf("Error parsing lxd_network_zone traversal: %s\n", diag.Error())
				}

				tokens := hclwrite.TokensForTraversal(tr)
				resourceBody.SetAttributeRaw("zone", tokens)
				l.Info(fmt.Sprintf("Adding 'zone' field to resource %q", node.Label), nil)
				continue
			}

			if dep.Label == "lxd_project" {
				rawExpr := fmt.Sprintf("lxd_project.%s.name", dep.ID)
				tr, diag := hclsyntax.ParseTraversalAbs([]byte(rawExpr), "", hcl.Pos{Line: 1, Column: 1})
				if diag.HasErrors() {
					return fmt.Errorf("Error parsing lxd_project traversal: %s\n", diag.Error())
				}

				tokens := hclwrite.TokensForTraversal(tr)
				resourceBody.SetAttributeRaw("project", tokens)
				l.Info(fmt.Sprintf("Adding 'project' field to resource %q", node.Label), nil)
			}
		}

		for k, v := range extraResAttr {
			resourceBody.SetAttributeValue(k, v)
			l.Info(fmt.Sprintf("Adding extra attribute %q to resource %q", k, node.Label), nil)
		}

		rootBody.AppendNewline()
	case "lxd_network":
		network := node.Data.(api.Network)
		if !network.Managed {
			l.Debug(fmt.Sprintf("Ignoring non-managed network %q", node.ID), nil)
			return nil
		}

		var parentNetwork string
		var projectTokens hclwrite.Tokens
		// A network like OVN might depends_on a managed bridge or external network.
		// If this is the case, we need to add the depends_on meta-attribute.
		for _, dep := range node.DependsOn {
			if dep.Label == "lxd_network" && network.Type == "ovn" {
				parentNetwork = fmt.Sprintf("lxd_network.%s.name", dep.ID)
				l.Info(fmt.Sprintf("Adding depends_on meta-attribute to network %q", node.ID), nil)
				continue
			}

			// A network has a project field.
			if dep.Label == "lxd_project" {
				rawExpr := fmt.Sprintf("lxd_project.%s.name", dep.ID)
				tr, diag := hclsyntax.ParseTraversalAbs([]byte(rawExpr), "", hcl.Pos{Line: 1, Column: 1})
				if diag.HasErrors() {
					return fmt.Errorf("Error parsing lxd_project traversal: %s\n", diag.Error())
				}

				l.Info(fmt.Sprintf("Adding project field to network %q", node.ID), nil)
				projectTokens = hclwrite.TokensForTraversal(tr)
			}
		}

		if len(network.Locations) == 0 {
			l.Error(fmt.Sprintf("Network %q has no locations", node.ID), nil)
			return fmt.Errorf("Network %q has no locations", node.ID)
		}

		if len(network.Locations) == 1 {
			resourceBlock := rootBody.AppendNewBlock("resource", []string{node.Label, node.ID})
			resourceBody := resourceBlock.Body()
			gohcl.EncodeIntoBody(node.Data, resourceBody)
			resourceBody.SetAttributeValue("remote", cty.StringVal(dstRemoteName))
			resourceBody.SetAttributeValue("target", cty.StringVal(network.Locations[0]))
			if parentNetwork != "" {
				resourceBody.SetAttributeValue("depends_on", cty.ListVal([]cty.Value{cty.StringVal(parentNetwork)}))
			}

			if projectTokens != nil {
				resourceBody.SetAttributeRaw("project", projectTokens)
			}

			for k, v := range extraResAttr {
				resourceBody.SetAttributeValue(k, v)
				l.Info(fmt.Sprintf("Adding extra attribute %q to resource %q", k, node.Label), nil)
			}

			rootBody.AppendNewline()
			l.Info(fmt.Sprintf("Adding resource %q with ID %q to HCL definition", node.Label, node.ID), nil)
			return nil
		}

		// Else, the network is on multiple locations.
		// Add the network resource for each location.
		perTargetDeps := make([]cty.Value, 0)
		for _, location := range network.Locations {
			resourceBlock := rootBody.AppendNewBlock("resource", []string{node.Label, fmt.Sprintf("%s_%s", node.ID, location)})
			resourceBody := resourceBlock.Body()
			resourceBody.SetAttributeValue("remote", cty.StringVal(dstRemoteName))
			resourceBody.SetAttributeValue("name", cty.StringVal(node.ID))
			resourceBody.SetAttributeValue("target", cty.StringVal(location))
			rootBody.AppendNewline()
			perTargetDeps = append(perTargetDeps, cty.StringVal(fmt.Sprintf("%s.%s", node.Label, fmt.Sprintf("%s_%s", node.ID, location))))
		}

		resourceBlock := rootBody.AppendNewBlock("resource", []string{node.Label, node.ID})
		resourceBody := resourceBlock.Body()
		gohcl.EncodeIntoBody(node.Data, resourceBody)
		resourceBody.AppendNewline()
		resourceBody.SetAttributeValue("remote", cty.StringVal(dstRemoteName))
		if parentNetwork != "" {
			perTargetDeps = append(perTargetDeps, cty.StringVal(parentNetwork))
		}

		if projectTokens != nil {
			resourceBody.SetAttributeRaw("project", projectTokens)
		}

		resourceBody.SetAttributeValue("depends_on", cty.ListVal(perTargetDeps))

		for k, v := range extraResAttr {
			resourceBody.SetAttributeValue(k, v)
			l.Info(fmt.Sprintf("Adding extra attribute %q to resource %q", k, node.Label), nil)
		}

		rootBody.AppendNewline()
	case "lxd_network_forward", "lxd_network_lb":
		resourceBlock := rootBody.AppendNewBlock("resource", []string{node.Label, node.ID})
		resourceBody := resourceBlock.Body()
		gohcl.EncodeIntoBody(node.Data, resourceBody)
		resourceBody.SetAttributeValue("remote", cty.StringVal(dstRemoteName))

		// Add the 'network' and 'project' fields to the resource body.
		for _, dep := range node.DependsOn {
			if dep.Label == "lxd_network" {
				rawExpr := fmt.Sprintf("lxd_network.%s.name", dep.ID)
				tr, diag := hclsyntax.ParseTraversalAbs([]byte(rawExpr), "", hcl.Pos{Line: 1, Column: 1})
				if diag.HasErrors() {
					return fmt.Errorf("Error parsing lxd_network traversal: %s\n", diag.Error())
				}

				tokens := hclwrite.TokensForTraversal(tr)
				resourceBody.SetAttributeRaw("network", tokens)
				continue
			}

			if dep.Label == "lxd_project" {
				rawExpr := fmt.Sprintf("lxd_project.%s.name", dep.ID)
				tr, diag := hclsyntax.ParseTraversalAbs([]byte(rawExpr), "", hcl.Pos{Line: 1, Column: 1})
				if diag.HasErrors() {
					return fmt.Errorf("Error parsing lxd_project traversal: %s\n", diag.Error())
				}

				tokens := hclwrite.TokensForTraversal(tr)
				resourceBody.SetAttributeRaw("project", tokens)
			}
		}

		for k, v := range extraResAttr {
			resourceBody.SetAttributeValue(k, v)
			l.Info(fmt.Sprintf("Adding extra attribute %q to resource %q", k, node.Label), nil)
		}

		rootBody.AppendNewline()
	case "lxd_network_peer":
		_, parts := humanIDDecode(node.ID)
		if len(parts) != 3 {
			return fmt.Errorf("Error decoding human ID format for network peer: %q, parts: %q\n", node.ID, parts)
		}

		srcProject := parts[0]
		srcNetwork := parts[1]

		l.Info(fmt.Sprintf("Adding resource %q with ID %q to HCL definition", node.Label, node.ID), nil)
		l.Info(fmt.Sprintf("Source project: %q, source network: %q", srcProject, srcNetwork), nil)

		resourceBlock := rootBody.AppendNewBlock("resource", []string{node.Label, node.ID})
		resourceBody := resourceBlock.Body()
		gohcl.EncodeIntoBody(node.Data, resourceBody)
		resourceBody.SetAttributeValue("remote", cty.StringVal(dstRemoteName))

		// Add 'source network' and 'target network' fields
		srcNetworkRawExpr := fmt.Sprintf("lxd_network.%s.name", srcNetwork)
		tr, diag := hclsyntax.ParseTraversalAbs([]byte(srcNetworkRawExpr), "", hcl.Pos{Line: 1, Column: 1})
		if diag.HasErrors() {
			return fmt.Errorf("Error parsing lxd_network traversal: %s\n", diag.Error())
		}

		tokens := hclwrite.TokensForTraversal(tr)
		resourceBody.SetAttributeRaw("source_network", tokens)

		data, ok := node.Data.(*api.NetworkPeer)
		if !ok {
			return fmt.Errorf("Error casting network peer data to *api.NetworkPeer\n")
		}

		dstNetworkRawExpr := fmt.Sprintf("lxd_network.%s.name", data.TargetNetwork)
		tr, diag = hclsyntax.ParseTraversalAbs([]byte(dstNetworkRawExpr), "", hcl.Pos{Line: 1, Column: 1})
		if diag.HasErrors() {
			return fmt.Errorf("Error parsing lxd_network traversal: %s\n", diag.Error())
		}

		tokens = hclwrite.TokensForTraversal(tr)
		resourceBody.SetAttributeRaw("target_network", tokens)

		if srcProject != "default" {
			rawExpr := fmt.Sprintf("lxd_project.%s.name", srcProject)
			tr, diag = hclsyntax.ParseTraversalAbs([]byte(rawExpr), "", hcl.Pos{Line: 1, Column: 1})
			if diag.HasErrors() {
				return fmt.Errorf("Error parsing lxd_project traversal: %s\n", diag.Error())
			}

			tokens = hclwrite.TokensForTraversal(tr)
			resourceBody.SetAttributeRaw("source_project", tokens)
		}

		targetProject := data.TargetProject
		if targetProject != srcProject {
			rawExpr := fmt.Sprintf("lxd_project.%s.name", targetProject)
			tr, diag = hclsyntax.ParseTraversalAbs([]byte(rawExpr), "", hcl.Pos{Line: 1, Column: 1})
			if diag.HasErrors() {
				return fmt.Errorf("Error parsing lxd_project traversal: %s\n", diag.Error())
			}

			tokens = hclwrite.TokensForTraversal(tr)
			resourceBody.SetAttributeRaw("target_project", tokens)
		}

		for k, v := range extraResAttr {
			resourceBody.SetAttributeValue(k, v)
			l.Info(fmt.Sprintf("Adding extra attribute %q to resource %q", k, node.Label), nil)
		}

		rootBody.AppendNewline()
	case "lxd_network_acl":
		resourceBlock := rootBody.AppendNewBlock("resource", []string{node.Label, node.ID})
		resourceBody := resourceBlock.Body()
		gohcl.EncodeIntoBody(node.Data, resourceBody)
		resourceBody.SetAttributeValue("remote", cty.StringVal(dstRemoteName))

		netPeerDeps := make([]cty.Value, 0)
		for _, dep := range node.DependsOn {
			if dep.Label == "lxd_project" {
				rawExpr := fmt.Sprintf("lxd_project.%s.name", dep.ID)
				tr, diag := hclsyntax.ParseTraversalAbs([]byte(rawExpr), "", hcl.Pos{Line: 1, Column: 1})
				if diag.HasErrors() {
					return fmt.Errorf("Error parsing lxd_project traversal: %s\n", diag.Error())
				}

				tokens := hclwrite.TokensForTraversal(tr)
				resourceBody.SetAttributeRaw("project", tokens)
				continue
			}

			if dep.Label == "lxd_network_peer" {
				netPeerDeps = append(netPeerDeps, cty.StringVal(fmt.Sprintf("lxd_network_peer.%s.name", dep.ID)))
			}
		}

		if len(netPeerDeps) > 0 {
			resourceBody.SetAttributeValue("depends_on", cty.ListVal(netPeerDeps))
		}

		for k, v := range extraResAttr {
			resourceBody.SetAttributeValue(k, v)
			l.Info(fmt.Sprintf("Adding extra attribute %q to resource %q", k, node.Label), nil)
		}

		rootBody.AppendNewline()
	}

	return nil
}

func addTFResources(rootBody *hclwrite.Body, inventory []*HCLResourceNode, dstRemoteName string, l *Logger) error {
	supportedLabels := getSupportedHCLLabels()
	l.Info("Supported HCL labels", map[string]any{"labels": supportedLabels})
	for _, node := range inventory {
		if !shared.ValueInSlice[string](node.Label, supportedLabels) {
			fmt.Printf("Unsupported resource type: %q . Ignoring.\n", node.Label)
			continue
		}

		err := addTFResource(rootBody, node, dstRemoteName, nil, l)
		if err != nil {
			return fmt.Errorf("Error adding resource to Terraform: %w", err)
		}
	}

	return nil
}

type tfState struct {
	Version          int          `json:"version"`
	TerraformVersion string       `json:"terraform_version"`
	Resources        []tfResource `json:"resources"`
}

type tfResource struct {
	Type      string       `json:"type"`
	Name      string       `json:"name"`
	Mode      string       `json:"mode"`
	Provider  string       `json:"provider"`
	Instances []tfInstance `json:"instances"`
}

type tfInstance struct {
	SchemaVersion       int            `json:"schema_version"`
	Attributes          map[string]any `json:"attributes"`
	SensitiveAttributes []any          `json:"sensitive_attributes"`
}

// In order to avoid adding a resource to the tf file that is already tracked in .tfstate file,
// we need to check if the resource is already tracked.
// This is only for non-bootstrap mode and is called during the diff phase. For that we need to load and parse the tfstate file which is just a JSON.
func readTFState(tfWorkDir string) (*tfState, error) {
	tfStatefilePath := filepath.Join(tfWorkDir, "terraform.tfstate")
	_, err := os.Stat(tfStatefilePath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("Terraform state file not found: %w", err)
	}

	data, err := os.ReadFile(tfStatefilePath)
	if err != nil {
		return nil, fmt.Errorf("Failed to read file: %w", err)
	}

	// Unmarshal JSON data into TFState struct
	var state tfState
	err = json.Unmarshal(data, &state)
	if err != nil {
		return nil, fmt.Errorf("Failed to unmarshal JSON data: %w", err)
	}

	return &state, nil
}

func convertDiffToHCLDef(rootBody *hclwrite.Body, allChangelog diff.Changelog, tfState *tfState, dstRemoteName string, l *Logger) error {
	isHCLResTracked := func(labelID string, ID string, remote string) bool {
		for _, res := range tfState.Resources {
			if res.Type == labelID && res.Name == ID {
				// check the remote attribute
				return res.Instances[0].Attributes["remote"] == remote
			}
		}

		return false
	}

	for _, change := range allChangelog {
		path := change.Path
		if len(path) == 1 {
			if change.Type == diff.CREATE {
				node, ok := change.To.(*HCLResourceNode)
				if !ok {
					return fmt.Errorf("Failed to cast to HCLResourceNode (create diff detected)")
				}

				// tracked resources are not added to the HCL definition
				if isHCLResTracked(node.Label, node.ID, dstRemoteName) {
					l.Debug(fmt.Sprintf("Resource %q with ID %q is already tracked in the Terraform state file", node.Label, node.ID), nil)
					continue
				}

				err := addTFResource(rootBody, node, dstRemoteName, map[string]cty.Value{"count": cty.NumberIntVal(0)}, l)
				if err != nil {
					return fmt.Errorf("Error adding resource to Terraform: %w", err)
				}
			}
		}
	}

	return nil
}

// getTFPathFromSnap loads the PATH environment variable from the host environment file
// so that these paths can be used to find the Terraform binary from inside a snap environment.
func getTFPathFromSnap() ([]string, error) {
	hostEnvironmentFile := "/var/lib/snapd/hostfs/etc/environment"

	entries := make([]string, 0)
	file, err := os.Open(hostEnvironmentFile)
	if err != nil {
		return nil, fmt.Errorf("Failed to open host environment file: %w", err)
	}

	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "PATH=") {
			path := strings.TrimPrefix(line, "PATH=")
			path = strings.Trim(path, "\"")
			for _, p := range strings.Split(path, ":") {
				entries = append(entries, fmt.Sprintf("/var/lib/snapd/hostfs%s", p))
			}

			return entries, nil
		}
	}

	err = scanner.Err()
	if err != nil {
		return nil, fmt.Errorf("Error reading host environment file: %w", err)
	}

	return nil, fmt.Errorf("PATH not found in host environment file")
}

func ClusterExport(
	srcClusterCert string,
	srcClusterKey string,
	srcClusterAddr string,
	srcClusterRemoteName string,
	bootstrap bool,
	tfWorkdir string,
	tfDefinitionFile string,
	verbose bool,
) error {
	logger := NewLogger(verbose, "export/inventory_building")
	srcInventory, err := getInventory(
		srcClusterCert,
		srcClusterKey,
		srcClusterAddr,
		logger,
	)
	if err != nil {
		return fmt.Errorf("Error getting source cluster inventory: %w", err)
	}

	// 2) Once we have the inventory, we can serialize it into an HCL definition.
	hclDef, rootBody, lxdProviderBody := rootHCLDefFile()

	// Set target remote block
	remoteDstBlock := lxdProviderBody.AppendNewBlock("remote", nil)
	remoteDstBody := remoteDstBlock.Body()
	remoteDstBody.SetAttributeValue("name", cty.StringVal(srcClusterRemoteName))
	remoteDstBody.SetAttributeValue("address", cty.StringVal(srcClusterAddr))

	logger.SetAllPrefixes("export/hcl_definition_building")

	if bootstrap {
		extraPaths := []string{}
		if shared.InSnap() {
			extraPaths, err = getTFPathFromSnap()
			if err != nil {
				return fmt.Errorf("Error getting Terraform path from snap: %w", err)
			}

			logger.Info("Detected snap environment, using extra paths for Terraform binary", map[string]any{"extra_paths": extraPaths})
		}

		tfVersion := &tfFS.AnyVersion{
			Product:    &tfProduct.Terraform,
			ExtraPaths: extraPaths,
		}
		tfExecPath, err := tfVersion.Find(context.Background())
		if err != nil {
			return fmt.Errorf("Error finding Terraform binary: %w", err)
		}

		// Instantiate Terraform (in bootstrap mode, the workdir is allowed to be empty. In such a case, we will bootstrap in the current directory).
		if tfWorkdir == "" {
			tfWorkdir, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("Error getting current working directory: %w", err)
			}
		}

		tf, err := tfexec.NewTerraform(tfWorkdir, tfExecPath)
		if err != nil {
			return fmt.Errorf("error creating Terraform instance: %w", err)
		}

		wr := &bytes.Buffer{}
		_, err = hclDef.WriteTo(wr)
		if err != nil {
			return fmt.Errorf("Error writing HCL definition to buffer: %w", err)
		}

		if tfDefinitionFile == "" {
			tfDefinitionFile = fmt.Sprintf("exported-%s.tf", srcClusterRemoteName)
		}

		// We need to write the HCL definition to a file a first time so that we can initialize Terraform
		// (so that the provider plugins is downloaded).
		err = os.WriteFile(filepath.Join(tfWorkdir, tfDefinitionFile), wr.Bytes(), 0644)
		if err != nil {
			return fmt.Errorf("Error writing HCL definition to file: %w", err)
		}

		logger.Info("Initializing Terraform with LXD provider...", nil)
		err = tf.Init(context.Background(), tfexec.Upgrade(true))
		if err != nil {
			return fmt.Errorf("Error initializing Terraform: %w", err)
		}
	}

	// We can now populate the HCL definition with the resources.
	err = addTFResources(rootBody, srcInventory, srcClusterRemoteName, logger)
	if err != nil {
		return fmt.Errorf("Error adding resources to Terraform: %w", err)
	}

	// Overwrite the HCL definition file with the new resources.
	wr := &bytes.Buffer{}
	_, err = hclDef.WriteTo(wr)
	if err != nil {
		return fmt.Errorf("Error writing HCL definition to buffer: %w", err)
	}

	err = os.WriteFile(filepath.Join(tfWorkdir, tfDefinitionFile), wr.Bytes(), 0644)
	if err != nil {
		return fmt.Errorf("Error writing HCL definition to file: %w", err)
	}

	return nil
}

func syncClusterTopologies(
	srcClusterCert string,
	srcClusterKey string,
	srcClusterAddr string,
	dstClusterCert string,
	dstClusterKey string,
	dstClusterAddr string,
	alignServers bool,
) (clusterAlignmentRequired bool, err error) {
	srcClient, err := lxdClient(
		srcClusterCert,
		srcClusterKey,
		srcClusterAddr,
	)
	if err != nil {
		return false, err
	}

	dstClient, err := lxdClient(
		dstClusterCert,
		dstClusterKey,
		dstClusterAddr,
	)
	if err != nil {
		return false, err
	}

	srcClusterTopo, err := newClusterTopology(srcClient)
	if err != nil {
		return false, fmt.Errorf("Error getting source cluster topology: %w", err)
	}

	dstClusterTopo, err := newClusterTopology(dstClient)
	if err != nil {
		return false, fmt.Errorf("Error getting destination cluster topology: %w", err)
	}

	alignments, diags, err := compareClusterTopologies(srcClusterTopo, dstClusterTopo, dstClient)
	if err != nil {
		return false, fmt.Errorf("Error comparing cluster topologies: %w", err)
	}

	fmt.Println(showAlignmentsAndDiags(diags, alignments))
	if alignServers {
		err = executeAlignments(alignments)
		if err != nil {
			return false, fmt.Errorf("Error executing alignments: %w", err)
		}

		return false, nil
	}

	if len(alignments) > 0 {
		return true, nil
	}

	return false, nil
}

// checkNetworkNICsCompatibility checks that all non-managed NICs in the source cluster that
// are used as parent for managed networks are present in the destination cluster.
func checkNetworkNICsCompatibility(srcInventory []*HCLResourceNode, dstInventory []*HCLResourceNode) error {
	srcNetworkNICs := make(map[string]struct{})
	dstNetworkNICs := make(map[string]struct{})
	getNetworkNICs := func(inventory []*HCLResourceNode, networkNICs map[string]struct{}, withNonManagedNets bool) error {
		for _, node := range inventory {
			if node.Label == "lxd_network" {
				network, ok := node.Data.(api.Network)
				if !ok {
					return fmt.Errorf("Error casting network data to api.Network\n")
				}

				if network.Managed && shared.ValueInSlice(network.Type, []string{"physical", "macvlan", "sriov"}) {
					deps := node.DependsOn
					for _, dep := range deps {
						if dep.Label == "lxd_network" {
							_, parts := humanIDDecode(dep.ID)
							if len(parts) != 2 {
								return fmt.Errorf("Error decoding human ID format for network: %q, parts: %q\n", dep.ID, parts)
							}

							networkNICs[parts[1]] = struct{}{}
						}
					}
				}

				if withNonManagedNets && !network.Managed {
					_, parts := humanIDDecode(node.ID)
					if len(parts) != 2 {
						return fmt.Errorf("Error decoding human ID format for network: %q, parts: %q\n", node.ID, parts)
					}

					networkNICs[parts[1]] = struct{}{}
				}
			}
		}

		return nil
	}

	err := getNetworkNICs(srcInventory, srcNetworkNICs, false)
	if err != nil {
		return fmt.Errorf("Error getting source network NICs: %w", err)
	}

	err = getNetworkNICs(dstInventory, dstNetworkNICs, true)
	if err != nil {
		return fmt.Errorf("Error getting destination network NICs: %w", err)
	}

	// Check that all NICs in the source cluster are present in the destination cluster.
	for nic := range srcNetworkNICs {
		_, ok := dstNetworkNICs[nic]
		if !ok {
			return fmt.Errorf("Network NIC %q present in source cluster but not in destination cluster", nic)
		}
	}

	return nil
}

func ClusterSync(
	srcClusterCert string,
	srcClusterKey string,
	srcClusterAddr string,
	srcClusterRemoteName string,
	dstClusterCert string,
	dstClusterKey string,
	dstClusterAddr string,
	dstClusterRemoteName string,
	bootstrap bool,
	tfWorkdir string,
	tfDefinitionFile string,
	autoPlan bool,
	autoApply bool,
	alignServers bool,
	verbose bool,
) error {
	// First, check the src and dst cluster 'topologies' (what they look like from a structural point of view).
	// We need to check that they have the same number of nodes, that the cluster members have the same names, etc.
	// This is essential to be able to properly create LXD resources like networks (that can span on multiple node locations like OVN).
	// e.g: An OVN network on src cluster having 'micro1', 'micro2' and 'micro3' can not be created on dst cluster with 'micro1', 'micro2' and 'micro4' members.
	// As of now, TF does not support this kind of operation, so we need to do it ourselves through the use of the LXD API.
	clusterAlignmentRequired, err := syncClusterTopologies(
		srcClusterCert,
		srcClusterKey,
		srcClusterAddr,
		dstClusterCert,
		dstClusterKey,
		dstClusterAddr,
		alignServers,
	)
	if err != nil {
		if alignServers {
			return fmt.Errorf("Error syncing cluster topologies: %w", err)
		}

		return fmt.Errorf("Error checking cluster topologies: %w", err)
	}

	// Then, make a resource inventory of the source cluster.
	// If we're in bootstrap mode, we only need the source cluster inventory, else we need both the source and destination cluster inventories
	// to be able to compare them and generate the HCL definition.
	var srcInventory []*HCLResourceNode
	var dstInventory []*HCLResourceNode
	if bootstrap {
		srcInventory, err = getInventory(
			srcClusterCert,
			srcClusterKey,
			srcClusterAddr,
			NewLogger(verbose, "sync/bootstrap_inventory_building"),
		)
		if err != nil {
			return fmt.Errorf("Error getting source cluster inventory: %w", err)
		}
	} else {
		srcInventory, dstInventory, err = getInventories(
			srcClusterAddr,
			srcClusterKey,
			srcClusterAddr,
			dstClusterCert,
			dstClusterKey,
			dstClusterAddr,
			verbose,
		)
		if err != nil {
			return fmt.Errorf("Error getting inventories: %w", err)
		}
	}

	// At this stage we can check that the managed network having parent NICs in SRC must exist in DST.
	err = checkNetworkNICsCompatibility(srcInventory, dstInventory)
	if err != nil {
		return fmt.Errorf("Error checking network NICs compatibility: %w", err)
	}

	// TODO: we must do the same for storage pools underlying source path (or block device) present in SRC cluster and not in DST cluster.
	// This is not yet implemented in the current version of the tool.
	// Challenge: how do we securely expose this information to the tool? The best idea so far is for the tool to have an ssh access
	// to all the members of the DST cluster to check if the source path or block devices are present on the members.

	logger := NewLogger(verbose, "sync/hcl_definition_building")

	// Once we have the inventory, we can serialize it into an HCL definition.
	hclDef, rootBody, lxdProviderBody := rootHCLDefFile()

	// Set target remote block
	remoteDstBlock := lxdProviderBody.AppendNewBlock("remote", nil)
	remoteDstBody := remoteDstBlock.Body()
	remoteDstBody.SetAttributeValue("name", cty.StringVal(dstClusterRemoteName))
	remoteDstBody.SetAttributeValue("address", cty.StringVal(dstClusterAddr))

	extraPaths := []string{}
	if shared.InSnap() {
		extraPaths, err = getTFPathFromSnap()
		if err != nil {
			return fmt.Errorf("Error getting Terraform path from snap: %w", err)
		}

		logger.Info("Detected snap environment, using extra paths for Terraform binary", map[string]any{"extra_paths": extraPaths})
	}

	tfVersion := &tfFS.AnyVersion{
		Product:    &tfProduct.Terraform,
		ExtraPaths: extraPaths,
	}
	tfExecPath, err := tfVersion.Find(context.Background())
	if err != nil {
		return fmt.Errorf("Error finding Terraform binary: %w", err)
	}

	var tf *tfexec.Terraform
	if bootstrap {
		// Instantiate Terraform (in bootstrap mode, the workdir is allowed to be empty. In such a case, we will bootstrap in the current directory).
		if tfWorkdir == "" {
			tfWorkdir, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("Error getting current working directory: %w", err)
			}
		}

		tf, err = tfexec.NewTerraform(tfWorkdir, tfExecPath)
		if err != nil {
			return fmt.Errorf("error creating Terraform instance: %w", err)
		}

		wr := &bytes.Buffer{}
		_, err = hclDef.WriteTo(wr)
		if err != nil {
			return fmt.Errorf("Error writing HCL definition to buffer: %w", err)
		}

		if tfDefinitionFile == "" {
			tfDefinitionFile = fmt.Sprintf("sync-%s-from-%s.tf", dstClusterRemoteName, srcClusterRemoteName)
		}

		// We need to write the HCL definition to a file a first time so that we can initialize Terraform
		// (so that the provider plugins is downloaded).
		err = os.WriteFile(filepath.Join(tfWorkdir, tfDefinitionFile), wr.Bytes(), 0644)
		if err != nil {
			return fmt.Errorf("Error writing HCL definition to file: %w", err)
		}

		err = tf.Init(context.Background(), tfexec.Upgrade(true))
		if err != nil {
			return fmt.Errorf("Error initializing Terraform: %w", err)
		}

		// We can now populate the HCL definition with the resources.
		err = addTFResources(rootBody, srcInventory, dstClusterRemoteName, logger)
		if err != nil {
			return fmt.Errorf("Error adding resources to Terraform: %w", err)
		}

		// Overwrite the HCL definition file with the new resources.
		wr = &bytes.Buffer{}
		_, err = hclDef.WriteTo(wr)
		if err != nil {
			return fmt.Errorf("Error writing HCL definition to buffer: %w", err)
		}

		err = os.WriteFile(filepath.Join(tfWorkdir, tfDefinitionFile), wr.Bytes(), 0644)
		if err != nil {
			return fmt.Errorf("Error writing HCL definition to file: %w", err)
		}
	} else {
		// Here, in a non-bootstrap scenario, we also need to add the TF resources but there is a slight twist:
		// the dst cluster (already established) might have 'drifted' and its real state might not match the state in the .tfstate file.
		// We could use 'imports' to sync the state with the remote but this could lead to the following issue:
		// - If a resource is present in the dst and not in src (and not tracked in tfstate), we need to remove it from the dst.
		// - Detecting this could be done with: create entity sets and check the discrepancies between src and dst clusters (use 'r3labs/diff' library and only check for ADD/DELETE edit at the index level in changelogs).
		// - If we 'import' the resource with `tf-exec import` to sync the state, after doing a plan/import, TF should detect the resources to be destroyed
		//   BUT the tfstate would have no information on the dependencies of the resources to be destroyed. TF would then proceed
		//   with destroying the resources in an arbitrary order during the apply phase which could lead to issues.
		// - For this reason, we prefer to add the to-be-destroyed resources in the HCL file with a meta-attribute 'count = 0'.
		// - This way, TF will know about the dependencies and will destroy the resources in the correct order during the 'apply' phase.
		// - After the apply, we'd just need to parse the HCL file and remove the resources with 'count = 0' from the HCL file. Doing
		//   a plan/apply again would then not change anything (this is just to have a cleaner HCL file for versioning purposes).

		// Populate the HCL definition with the src cluster resources.
		err = addTFResources(rootBody, srcInventory, dstClusterRemoteName, logger)
		if err != nil {
			return fmt.Errorf("Error adding resources to Terraform: %w", err)
		}

		// Load tfstate file
		tfState, err := readTFState(tfWorkdir)
		if err != nil {
			return fmt.Errorf("Error reading Terraform state file: %w", err)
		}

		logger.Info("Terraform state file loaded", map[string]any{"tfstate": tfState})

		// Detect the differences between the source and destination inventories.
		allChangelogs, err := inventoriesChangelogs(srcInventory, dstInventory)
		if err != nil {
			return fmt.Errorf("Error computing diff between source and destination inventories: %w", err)
		}

		// Resources that are detected as present in the src but
		// not in the dst should be added to the HCL definition with 'count = 0' (if they are not tracked in the tfstate file).
		err = convertDiffToHCLDef(rootBody, allChangelogs, tfState, dstClusterRemoteName, logger)
		if err != nil {
			return fmt.Errorf("Error applying diff to HCL definition: %w", err)
		}

		// Overwrite the HCL definition file with the new resources.
		wr := &bytes.Buffer{}
		_, err = hclDef.WriteTo(wr)
		if err != nil {
			return fmt.Errorf("Error writing HCL definition to buffer: %w", err)
		}

		err = os.WriteFile(filepath.Join(tfWorkdir, tfDefinitionFile), wr.Bytes(), 0644)
		if err != nil {
			return fmt.Errorf("Error writing HCL definition to file: %w", err)
		}
	}

	if autoPlan {
		tfPlanDiff, err := tf.PlanJSON(context.Background(), os.Stdout)
		if err != nil {
			return fmt.Errorf("Error running terraform plan: %v", err)
		}

		if tfPlanDiff {
			if clusterAlignmentRequired && autoApply {
				return fmt.Errorf("Cluster topologies are not aligned. Please align them first before TF can apply changes.")
			}

			if autoApply {
				err = tf.ApplyJSON(context.Background(), os.Stdout)
				if err != nil {
					return fmt.Errorf("Error running Terraform apply: %w", err)
				}
			}
		}
	}

	return nil
}
