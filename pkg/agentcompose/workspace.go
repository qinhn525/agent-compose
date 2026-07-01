package agentcompose

import "agent-compose/pkg/agentcompose/workspaces"

const gitWorkspaceTempDirName = workspaces.GitWorkspaceTempDirName

const fileWorkspaceContentDirName = workspaces.FileWorkspaceContentDirName

type gitWorkspaceConfig = workspaces.GitWorkspaceConfig
type fileWorkspaceConfig = workspaces.FileWorkspaceConfig
type fileWorkspaceContent = workspaces.FileWorkspaceContent
type workspaceFileEntry = workspaces.FileEntry
