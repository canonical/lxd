package importer

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd-export/core/nodes"
	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	nodeConfig "github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/r3labs/diff/v3"
	"gonum.org/v1/gonum/graph/simple"
)

// Edit represents a single edit operation in the plan.
type Edit struct {
	Desc   string
	Rank   int
	Fn     func() error
	Pretty string
}

func NewEdit(desc string, rank int, payload any, f func() error) Edit {
	pretty := ""
	if payload != nil {
		pretty = logger.Pretty(payload)
	}

	return Edit{
		Desc:   desc,
		Rank:   rank,
		Fn:     f,
		Pretty: pretty,
	}
}

// Plan represents a sequence of Edit to transform a target DAG into a source DAG.
// A plan is actually "batched": each edit has a rank that indicate it can be executed in parallel.
// plan = [batch0, batch1, ...]
// batch0 = [edit01, edit01, ...]
// batch1 = [edit10, edit11, ...]
// ...
//
// A plan also comes with diagnostics: a human-readable list of what is going to happen when the plan is executed.
type Plan struct {
	Steps []Edit
	Diags Diagnostics
}

func (p *Plan) String() string {
	var reset = "\033[0m"
	var red = "\033[31m"
	var green = "\033[32m"
	var yellow = "\033[33m"
	var blue = "\033[34m"

	out := ""
	// First, show diagnostics if any.
	if len(p.Diags) > 0 {
		out = red + p.Diags.String() + reset
		out += "\n\n"
	}

	if len(p.Steps) != 0 {
		out += green + "PLAN:" + reset + "\n\n"
	} else {
		out += green + "No plan" + reset + "\n"
		return out
	}

	// Then, show the plan.
	localRank := 0
	sort.Slice(p.Steps, func(i, j int) bool {
		return p.Steps[i].Rank < p.Steps[j].Rank
	})

	out += yellow + "- Step 0:" + reset + "\n"

	for _, edit := range p.Steps {
		if edit.Rank != localRank {
			out += fmt.Sprintf(yellow+"- Step %d:"+reset+"\n", edit.Rank)
		}

		localRank = edit.Rank
		out += edit.Desc
		if edit.Pretty != "" {
			out += fmt.Sprintf(blue+"\t%s"+reset+"\n", edit.Pretty)
		}

		out += "\n"
	}

	return out
}

// Apply executes the plan.
//
// Each edit with the same rank can be executed in parallel. In order to pass to the edits with the next rank, all the edits
// with the current rank must be executed. If this batch of edits with the same rank has an error, the plan will error out without processing
// the next batch of edits.
func (p *Plan) Apply() error {
	sort.Slice(p.Steps, func(i, j int) bool {
		return p.Steps[i].Rank < p.Steps[j].Rank
	})

	wgCount := make(map[int]int)
	for _, edit := range p.Steps {
		_, ok := wgCount[edit.Rank]
		if !ok {
			wgCount[edit.Rank] = 1
		} else {
			wgCount[edit.Rank]++
		}
	}

	wgs := make(map[int]*sync.WaitGroup)
	errChans := make(map[int]chan error)
	for rank, count := range wgCount {
		errChans[rank] = make(chan error, count)
		wgs[rank] = &sync.WaitGroup{}
		wgs[rank].Add(count)

		go func() {
			wgs[rank].Wait()
			close(errChans[rank])
		}()
	}

	localRank := 0
	var errorsGroup []error
	for eIdx, edit := range p.Steps {
		if edit.Rank != localRank {
			for err := range errChans[localRank] {
				errorsGroup = append(errorsGroup, err)
			}

			if len(errorsGroup) != 0 {
				formattedErrors := ""
				for _, err := range errorsGroup {
					formattedErrors += fmt.Sprintf("- %v\n", err)
				}

				return fmt.Errorf("Errors during execution of rank %d:\n%s", localRank, formattedErrors)
			}

			localRank = edit.Rank
			wgs[localRank].Add(wgCount[localRank])
		}

		go func() {
			defer wgs[localRank].Done()
			fmt.Printf("Executing edit (rank: %d, edit: %d): %s\n", localRank, eIdx, edit.Desc)
			err := edit.Fn()
			if err != nil {
				formattedError := fmt.Errorf("Error executing edit (rank: %d, edit: %d): %v", localRank, eIdx, err)
				errChans[localRank] <- formattedError
				return
			}

			fmt.Printf("Edit (rank: %d, edit: %d) executed successfully\n", localRank, eIdx)
		}()
	}

	return nil
}

// NewPlan creates a new Plan.
func NewPlan() *Plan {
	return &Plan{
		Steps: make([]Edit, 0),
		Diags: make(Diagnostics, 0),
	}
}

// Planner is responsible for generating a plan.
type Planner struct {
	// Source DAG (the one we are importing from)
	srcDAG     *simple.DirectedGraph
	srcHIDtoID map[string]int64

	// Target DAG (the one that represent the current state of the cluster)
	// We will try to transform this DAG into the source DAG
	dstDAG     *simple.DirectedGraph
	dstHIDtoID map[string]int64

	// Client to interact with the target cluster
	client lxd.InstanceServer
}

// NewPlanner creates a new Planner.
func NewPlanner(srcDAG *simple.DirectedGraph, srcHIDtoID map[string]int64, dstDAG *simple.DirectedGraph, dstHIDtoID map[string]int64, client lxd.InstanceServer) *Planner {
	return &Planner{
		srcDAG:     srcDAG,
		srcHIDtoID: srcHIDtoID,
		dstDAG:     dstDAG,
		dstHIDtoID: dstHIDtoID,
		client:     client,
	}
}

func (p *Planner) rootNodeEdits(d diff.Changelog, tRoot *nodes.RootNode, stepIdx *int) (edits []Edit, diags Diagnostics, err error) {
	tRootData, ok := tRoot.Data().(nodes.RootData)
	if !ok {
		return nil, nil, fmt.Errorf("Failed to cast target root data to RootData")
	}

	// - critical diff found: we need to error out as the reconciliation needs to change the
	// server configuration themselves.
	// - warning diff found: the warning will be logged as a path of the diagnostics but no actions
	// will be taken to reconcile the differences as these are difference at the root node level. These are considered ok
	// to be different between the source and target clusters but the user should be aware of them.
	edits = make([]Edit, 0)

	// Split the global root changelog into cluster groups, cluster members and server configs changelogs.
	clusterGroupChanges := make([]diff.Change, 0)
	clusterMemberChanges := make([]diff.Change, 0)
	serverConfigChanges := make([]diff.Change, 0)
	for _, change := range d {
		switch change.Path[0] {
		case "cluster_groups":
			clusterGroupChanges = append(clusterGroupChanges, change)
		case "cluster_members":
			clusterMemberChanges = append(clusterMemberChanges, change)
		case "server_configs":
			serverConfigChanges = append(serverConfigChanges, change)
		default:
			return nil, nil, fmt.Errorf("Unknown root node diff path %q", change.Path[0])
		}
	}

	diags = make([]Diagnostic, 0)

	// First, process the server config changes as some changes might be critical and we might error out early.
	criticalFound := false
	globalServerPut := api.ServerPut{Config: make(map[string]any)}
	localServerPuts := make(map[string]*api.ServerPut)
	for _, change := range serverConfigChanges {
		if len(change.Path) == 2 {
			if change.Type == diff.DELETE {
				return nil, nil, fmt.Errorf("Cannot have a less servers on the target cluster than on the source cluster")
			}

			return nil, nil, fmt.Errorf("Cannot have more servers on the target cluster than on the source cluster")
		}

		// Check if the diff path has critical differences
		formattedDiffPath, critical, err := isDiffPathCritical(change.Path)
		if err != nil {
			return nil, nil, err
		}

		// If there are critical differences, set criticalFound to true and add the diagnostic to the diagnostics list.
		// We'll error out at the end but we want to keep track of all the critical/warning differences that make up a diagnostic.
		if critical {
			change.Path = formattedDiffPath
			diags = append(diags, NewDiagnostic(change, Critical))
			criticalFound = true
		}

		formattedDiffPath, warning, err := isDiffPathWarning(change.Path)
		if err != nil {
			return nil, nil, err
		}

		if warning {
			change.Path = formattedDiffPath
			diags = append(diags, NewDiagnostic(change, Warning))
		}

		if criticalFound {
			continue
		}

		// Process 'config' changes in the server configs.
		if change.Path[3] == "config" {
			configKey := change.Path[4]
			v, ok := clusterConfig.ConfigSchema[configKey]
			if ok {
				// This is a global scope config key.
				switch change.Type {
				case diff.UPDATE:
					globalServerPut.Config[configKey] = change.From.(string)
				case diff.DELETE:
					globalServerPut.Config[configKey] = change.From.(string)
				case diff.CREATE:
					globalServerPut.Config[configKey] = v.Default
				}
			}

			v, ok = nodeConfig.ConfigSchema[configKey]
			if ok {
				// Get target name
				idxServer, err := strconv.Atoi(change.Path[1])
				if err != nil {
					return nil, nil, err
				}

				localServerPut, ok := localServerPuts[tRootData.ServerConfigs[idxServer].Environment.ServerName]
				if !ok {
					localServerPut = &api.ServerPut{Config: make(map[string]any)}
					localServerPuts[tRootData.ServerConfigs[idxServer].Environment.ServerName] = localServerPut
				}

				// This is a local scope config key.
				switch change.Type {
				case diff.UPDATE:
					localServerPut.Config[configKey] = change.From.(string)
				case diff.DELETE:
					localServerPut.Config[configKey] = change.From.(string)
				case diff.CREATE:
					localServerPut.Config[configKey] = v.Default
				}
			}
		}
	}

	if criticalFound {
		return nil, nil, fmt.Errorf("Critical differences found in the root node:\n%s", Diagnostics(diags).String())
	}

	// Create the server edits.
	edits = append(edits, NewEdit("Update global server config", *stepIdx, globalServerPut, func() error {
		err = p.client.UpdateServer(globalServerPut, "")
		if err != nil {
			return err
		}

		return nil
	}))

	for serverName, serverPut := range localServerPuts {
		edits = append(edits, NewEdit(fmt.Sprintf("Update local server %q config", serverName), *stepIdx, *serverPut, func() error {
			targetedClient := p.client.UseTarget(serverName)

			err = targetedClient.UpdateServer(*serverPut, "")
			if err != nil {
				return err
			}

			return nil
		}))
	}

	// Then, process the cluster group changes as some cluster members might depend on the groups.
	clusterGroupsToUpdate := make(map[string]api.ClusterGroupPut, 0)
	clusterGroupsToAdd := make([]api.ClusterGroupsPost, 0)
	clusterGroupsToDelete := make([]string, 0)
	clusterGroupsToRename := make(map[string]string, 0)
	for _, change := range clusterGroupChanges {
		if len(change.Path) == 2 {
			switch change.Type {
			case diff.DELETE:
				group, ok := change.From.(api.ClusterGroup)
				if !ok {
					return nil, nil, fmt.Errorf("Failed to cast cluster group to ClusterGroupsPost (delete diff detected)")
				}

				clusterGroupsToAdd = append(
					clusterGroupsToAdd,
					api.ClusterGroupsPost{
						ClusterGroupPut: api.ClusterGroupPut{
							Description: group.Description,
							Members:     group.Members,
						},
						Name: group.Name,
					},
				)
			case diff.CREATE:
				group, ok := change.To.(api.ClusterGroup)
				if !ok {
					return nil, nil, fmt.Errorf("Failed to cast cluster group to ClusterGroupsPost (create diff detected)")
				}

				clusterGroupsToDelete = append(clusterGroupsToDelete, group.Name)
			}
		} else if len(change.Path) == 3 {
			idxClusterGroup, err := strconv.Atoi(change.Path[1])
			if err != nil {
				return nil, nil, err
			}

			name := tRootData.ClusterGroups[idxClusterGroup].Name
			clusterGroupToUpdate, ok := clusterGroupsToUpdate[name]
			if !ok {
				clusterGroupToUpdate = api.ClusterGroupPut{}
			}

			switch change.Path[2] {
			case "name":
				switch change.Type {
				case diff.UPDATE:
					clusterGroupsToRename[name] = change.From.(string)
				}
			case "description":
				switch change.Type {
				case diff.UPDATE, diff.DELETE:
					description := change.From.(string)
					clusterGroupToUpdate.Description = description
					clusterGroupsToUpdate[name] = clusterGroupToUpdate
				case diff.CREATE:
					description := ""
					clusterGroupToUpdate.Description = description
					clusterGroupsToUpdate[name] = clusterGroupToUpdate
				}
			case "members":
				idxMember, err := strconv.Atoi(change.Path[3])
				if err != nil {
					return nil, nil, err
				}

				switch change.Type {
				case diff.UPDATE:
					members := tRootData.ClusterGroups[idxClusterGroup].Members
					members[idxMember] = change.From.(string)
					clusterGroupToUpdate.Members = members
					clusterGroupsToUpdate[name] = clusterGroupToUpdate
				case diff.DELETE:
					members := tRootData.ClusterGroups[idxClusterGroup].Members
					members = append(members[:idxMember], change.From.(string))
					members = append(members, members[idxMember+1:]...)
					clusterGroupToUpdate.Members = members
					clusterGroupsToUpdate[name] = clusterGroupToUpdate
				case diff.CREATE:
					members := tRootData.ClusterGroups[idxClusterGroup].Members
					members = append(members[:idxMember], members[idxMember+1:]...)
					clusterGroupToUpdate.Members = members
					clusterGroupsToUpdate[name] = clusterGroupToUpdate
				}
			}
		}
	}

	// Create the cluster group edits.
	for _, group := range clusterGroupsToAdd {
		edits = append(edits, NewEdit(fmt.Sprintf("Add cluster group %q", group.Name), *stepIdx, group, func() error {
			err = p.client.CreateClusterGroup(group)
			if err != nil {
				return err
			}

			return nil
		}))
	}

	for _, group := range clusterGroupsToDelete {
		edits = append(edits, NewEdit(fmt.Sprintf("Delete cluster group %q", group), *stepIdx, group, func() error {
			err = p.client.DeleteClusterGroup(group)
			if err != nil {
				return err
			}

			return nil
		}))
	}

	for name, group := range clusterGroupsToUpdate {
		edits = append(edits, NewEdit(fmt.Sprintf("Update cluster group %q", name), *stepIdx, group, func() error {
			err = p.client.UpdateClusterGroup(name, group, "")
			if err != nil {
				return err
			}

			return nil
		}))
	}

	if len(clusterGroupsToRename) != 0 {
		*stepIdx++
	}

	for name, newName := range clusterGroupsToRename {
		edits = append(edits, NewEdit(fmt.Sprintf("Rename cluster group %q to %q", name, newName), *stepIdx, nil, func() error {
			err = p.client.RenameClusterGroup(name, api.ClusterGroupPost{Name: newName})
			if err != nil {
				return err
			}

			return nil
		}))
	}

	// Finally, process the cluster member changes.
	clusterMemberPuts := make(map[string]api.ClusterMemberPut, 0)
	clusterMemberRename := make(map[string]string, 0)
	for _, change := range clusterMemberChanges {
		idxClusterMem, err := strconv.Atoi(change.Path[1])
		if err != nil {
			return nil, nil, err
		}

		serverName := tRootData.ClusterMembers[idxClusterMem].ServerName
		clusterMemberPut, ok := clusterMemberPuts[serverName]
		if !ok {
			clusterMemberPut = api.ClusterMemberPut{}
		}

		switch change.Path[2] {
		case "server_name":
			if change.Type == diff.UPDATE {
				clusterMemberRename[serverName] = change.From.(string)
			}
		case "description":
			switch change.Type {
			case diff.UPDATE, diff.DELETE:
				clusterMemberPut.Description = change.From.(string)
				clusterMemberPuts[serverName] = clusterMemberPut
			case diff.CREATE:
				clusterMemberPut.Description = ""
				clusterMemberPuts[serverName] = clusterMemberPut
			}
		case "failure_domain":
			switch change.Type {
			case diff.UPDATE, diff.DELETE:
				clusterMemberPut.FailureDomain = change.From.(string)
				clusterMemberPuts[serverName] = clusterMemberPut
			case diff.CREATE:
				clusterMemberPut.FailureDomain = "default"
				clusterMemberPuts[serverName] = clusterMemberPut
			}
		case "roles":
			idxClusterRoles, err := strconv.Atoi(change.Path[4])
			if err != nil {
				return nil, nil, err
			}

			switch change.Type {
			case diff.UPDATE:
				roles := tRootData.ClusterMembers[idxClusterMem].Roles
				roles[idxClusterRoles] = change.From.(string)
				clusterMemberPut.Roles = roles
				clusterMemberPuts[serverName] = clusterMemberPut
			case diff.DELETE:
				roles := tRootData.ClusterMembers[idxClusterMem].Roles
				roles = append(roles[:idxClusterRoles], change.From.(string))
				roles = append(roles, roles[idxClusterRoles+1:]...)
				clusterMemberPut.Roles = roles
				clusterMemberPuts[serverName] = clusterMemberPut
			case diff.CREATE:
				roles := tRootData.ClusterMembers[idxClusterMem].Roles
				roles = append(roles[:idxClusterRoles], roles[idxClusterRoles+1:]...)
				clusterMemberPut.Roles = roles
				clusterMemberPuts[serverName] = clusterMemberPut
			}
		case "config":
			key := change.Path[4]
			if clusterMemberPut.Config == nil {
				clusterMemberPut.Config = make(map[string]string)
			}

			switch change.Type {
			case diff.UPDATE, diff.DELETE:
				clusterMemberPut.Config[key] = change.From.(string)
				clusterMemberPuts[serverName] = clusterMemberPut
			case diff.CREATE:
				clusterMemberPut.Config[key] = ""
				clusterMemberPuts[serverName] = clusterMemberPut
			}
		case "groups":
			idxClusterGroups, err := strconv.Atoi(change.Path[4])
			if err != nil {
				return nil, nil, err
			}

			switch change.Type {
			case diff.UPDATE:
				groups := tRootData.ClusterMembers[idxClusterMem].Groups
				groups[idxClusterGroups] = change.From.(string)
				clusterMemberPut.Groups = groups
				clusterMemberPuts[serverName] = clusterMemberPut
			case diff.DELETE:
				groups := tRootData.ClusterMembers[idxClusterMem].Groups
				groups = append(groups[:idxClusterGroups], change.From.(string))
				groups = append(groups, groups[idxClusterGroups+1:]...)
				clusterMemberPut.Groups = groups
				clusterMemberPuts[serverName] = clusterMemberPut
			case diff.CREATE:
				groups := tRootData.ClusterMembers[idxClusterMem].Groups
				groups = append(groups[:idxClusterGroups], groups[idxClusterGroups+1:]...)
				clusterMemberPut.Groups = groups
				clusterMemberPuts[serverName] = clusterMemberPut
			}
		}
	}

	if len(clusterMemberPuts) != 0 {
		*stepIdx++
	}

	// Create the cluster member edits.
	for serverName, memberPut := range clusterMemberPuts {
		edits = append(edits, NewEdit(fmt.Sprintf("Update cluster member %q", serverName), *stepIdx, memberPut, func() error {
			err = p.client.UpdateClusterMember(serverName, memberPut, "")
			if err != nil {
				return err
			}

			return nil
		}))
	}

	if len(clusterMemberRename) != 0 {
		*stepIdx++
	}

	for serverName, newName := range clusterMemberRename {
		edits = append(edits, NewEdit(fmt.Sprintf("Rename cluster member %q to %q", serverName, newName), *stepIdx, nil, func() error {
			err = p.client.RenameClusterMember(serverName, api.ClusterMemberPost{ServerName: newName})
			if err != nil {
				return err
			}

			return nil
		}))
	}

	*stepIdx++
	return edits, diags, nil
}

func (p *Planner) compareRootNodes(plan *Plan, stepIdx *int) error {
	sRoot, tRoot, err := queryNodePair[*nodes.RootNode](p, "root", "root")
	if err != nil {
		return err
	}

	d, err := sRoot.Diff(tRoot)
	if err != nil {
		return err
	}

	if d != nil {
		// Roots are different, can we try to apply the source root node to the target root node?
		rootSteps, diags, err := p.rootNodeEdits(d, tRoot, stepIdx)
		if err != nil {
			return err
		}

		plan.Steps = append(plan.Steps, rootSteps...)
		plan.Diags = append(plan.Diags, diags...)
	}

	return nil
}

func (p *Planner) getProjectsSet(sourceDAG bool) ([]*nodes.ProjectNode, error) {
	projects := make([]*nodes.ProjectNode, 0)
	var hidToGraphID map[string]int64
	var dag *simple.DirectedGraph
	if sourceDAG {
		hidToGraphID = p.srcHIDtoID
		dag = p.srcDAG
	} else {
		hidToGraphID = p.dstHIDtoID
		dag = p.dstDAG
	}

	for hid, id := range hidToGraphID {
		if strings.HasPrefix(hid, "project_") {
			projectNode, ok := dag.Node(id).(*nodes.ProjectNode)
			if !ok {
				return nil, fmt.Errorf("Failed to cast source project node to ProjectNode")
			}

			projects = append(projects, projectNode)
		}
	}

	return projects, nil
}

func (p *Planner) compareProjectNodes(plan *Plan, srcProjects, dstProjects []*nodes.ProjectNode, stepIdx *int) error {
	differ, err := diff.NewDiffer(diff.DisableStructValues())
	if err != nil {
		return fmt.Errorf("Failed to create differ for project nodes: %w", err)
	}

	changelog, err := differ.Diff(srcProjects, dstProjects)
	if err != nil {
		return fmt.Errorf("Failed to diff project nodes: %w", err)
	}

	// TODO: for now we only handle project addition and update. We don't handle project deletion as it implies
	// deep changes in the dependencies graph that are not well understood yet.
	projectsToCreate := make([]api.ProjectsPost, 0)
	projectsToUpdate := make(map[string]*api.ProjectPut, 0)
	for _, change := range changelog {
		if len(change.Path) == 1 {
			switch change.Type {
			case diff.CREATE:
				// WARNING: we don't handle project deletion for now.
				plan.Diags = append(plan.Diags, NewDiagnostic(change, Warning))
			case diff.DELETE:
				projectNode, ok := change.From.(nodes.ProjectNode)
				if !ok {
					return fmt.Errorf("Failed to cast to ProjectNode (delete diff detected)")
				}

				project, ok := projectNode.Data().(api.Project)
				if !ok {
					return fmt.Errorf("Failed to cast project data to api.Project (delete diff detected)")
				}

				projectsToCreate = append(
					projectsToCreate,
					api.ProjectsPost{
						ProjectPut: api.ProjectPut{
							Description: project.Description,
							Config:      project.Config,
						},
						Name: project.Name,
					},
				)
			}
		} else if len(change.Path) == 2 {
			idxProject, err := strconv.Atoi(change.Path[1])
			if err != nil {
				return err
			}

			dstProject := dstProjects[idxProject]
			name := dstProject.Name
			projectPut, ok := projectsToUpdate[name]
			if !ok {
				projectPut = &api.ProjectPut{}
			}

			switch change.Path[2] {
			case "description":
				switch change.Type {
				case diff.CREATE:
					projectPut.Description = ""
				case diff.UPDATE:
					projectPut.Description = change.From.(string)
				case diff.DELETE:
					projectPut.Description = change.From.(string)
				}
			default:
				// WARNING: we don't handle 'project config' operations for now.
				plan.Diags = append(plan.Diags, NewDiagnostic(change, Warning))
			}
		}
	}

	for _, project := range projectsToCreate {
		plan.Steps = append(plan.Steps, NewEdit(fmt.Sprintf("Create projects %s", project.Name), *stepIdx, projectsToCreate, func() error {
			err = p.client.CreateProject(project)
			if err != nil {
				return err
			}

			return nil
		}))
	}

	*stepIdx++

	for name, projectPut := range projectsToUpdate {
		plan.Steps = append(plan.Steps, NewEdit(fmt.Sprintf("Update project %s", name), *stepIdx, projectPut, func() error {
			err = p.client.UpdateProject(name, *projectPut, "")
			if err != nil {
				return err
			}

			return nil
		}))
	}

	return nil
}

// GeneratePlan generates a plan to transform the target DAG into the source DAG.
func (p *Planner) GeneratePlan() (*Plan, error) {
	plan := NewPlan()
	stepIdx := 0

	// First and foremost, we need to compare the root nodes of the source and target DAGs.
	// If there are some critical differences, we need to error out early. This function can also returns
	// a list of diagnostic: a diagnostic is an indication of a critical (if there is one, we error out),
	// warning (we want to let the user know but we don't error out) or other difference (written in a human-readable way).
	// These diagnostic will be integrated in the plan to let the user know what is going to happen when the plan execute.
	err := p.compareRootNodes(plan, &stepIdx)
	if err != nil {
		return nil, err
	}

	// Then compare source project set to target project set
	srcProjects, err := p.getProjectsSet(true)
	if err != nil {
		return nil, err
	}

	dstProjects, err := p.getProjectsSet(false)
	if err != nil {
		return nil, err
	}

	err = p.compareProjectNodes(plan, srcProjects, dstProjects, &stepIdx)
	if err != nil {
		return nil, err
	}

	// TODO: compare other entity sets

	return plan, nil
}
