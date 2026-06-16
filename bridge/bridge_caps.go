package bridge

import (
	"os"
	"sort"

	"github.com/chenhg5/cc-connect/core"
)

const (
	bridgeCapabilitiesSnapshotType  = "capabilities_snapshot"
	bridgeCapabilitiesSnapshotProto = "capabilities_snapshot_v1"
	bridgeCommandArgsModeText       = "text"
	bridgeCommandSourceBuiltin      = "builtin"
	bridgeCommandSourceCustom       = "custom"
)

// CurrentCommit is set by main at startup so bridge clients can inspect the
// host binary that produced a capability snapshot.
var CurrentCommit string

// CurrentBuildTime is set by main at startup so bridge clients can compare
// host snapshots without reverse-engineering git-describe version strings.
var CurrentBuildTime string

type bridgeCapabilitiesSnapshot struct {
	Type     string                      `json:"type"`
	Version  int                         `json:"v"`
	Host     bridgeCapabilitiesHost      `json:"host"`
	Projects []bridgeProjectCapabilities `json:"projects"`
}

type bridgeCapabilitiesHost struct {
	ID               string `json:"id"`
	Hostname         string `json:"hostname,omitempty"`
	CCConnectVersion string `json:"cc_connect_version,omitempty"`
	Commit           string `json:"commit,omitempty"`
	BuildTime        string `json:"build_time,omitempty"`
}

type bridgeProjectCapabilities struct {
	Project  string                   `json:"project"`
	Commands []bridgePublishedCommand `json:"commands"`
}

type bridgePublishedCommand struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	Source            string `json:"source"`
	RequiresWorkspace bool   `json:"requires_workspace"`
	ArgsMode          string `json:"args_mode"`
}

func (bs *BridgeServer) buildCapabilitiesSnapshot() bridgeCapabilitiesSnapshot {
	hostName, _ := os.Hostname()
	projects := make([]bridgeProjectCapabilities, 0, len(bs.engines))

	bs.enginesMu.RLock()
	projectNames := make([]string, 0, len(bs.engines))
	for projectName := range bs.engines {
		projectNames = append(projectNames, projectName)
	}
	sort.Strings(projectNames)
	for _, projectName := range projectNames {
		ref := bs.engines[projectName]
		if ref == nil || ref.engine == nil {
			continue
		}
		published := ref.engine.GetBridgePublishedCommands()
		cmds := make([]bridgePublishedCommand, len(published))
		for i, p := range published {
			cmds[i] = bridgePublishedCommand{
				Name:              p.Name,
				Description:       p.Description,
				Source:            p.Source,
				RequiresWorkspace: p.RequiresWorkspace,
				ArgsMode:          p.ArgsMode,
			}
		}
		projects = append(projects, bridgeProjectCapabilities{
			Project:  projectName,
			Commands: cmds,
		})
	}
	bs.enginesMu.RUnlock()

	return bridgeCapabilitiesSnapshot{
		Type:    bridgeCapabilitiesSnapshotType,
		Version: 1,
		Host: bridgeCapabilitiesHost{
			ID:               hostName,
			Hostname:         hostName,
			CCConnectVersion: core.CurrentVersion,
			Commit:           CurrentCommit,
			BuildTime:        CurrentBuildTime,
		},
		Projects: projects,
	}
}
