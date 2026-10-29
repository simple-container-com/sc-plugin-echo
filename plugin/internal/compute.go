// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package echo

import (
	"fmt"

	scPlugin "github.com/simple-container-com/api/pkg/plugin"
	scpluginv1 "github.com/simple-container-com/api/pkg/plugin/proto/v1"
)

// ComputeProcess is the echo stub for the ComputeProcess RPC (Phase 1e,
// ComputeProcessRequest/Response v2). A real plugin's ComputeProcessor
// turns a resource's provisioning outputs into the shape consuming
// stacks need — env vars, template extensions, dependency placeholders.
//
// The echo stub exercises the full ComputeResultBuilder surface:
//
//   - one plain env var,
//   - one secret env var,
//   - one resource template extension,
//   - one dependency template extension.
//
// What is NOT demonstrated here (not representable on the bus — see
// pkg/plugin/compute.go doc): AddDependency(sdk.Resource), AddOutput,
// pre/post-processors. Those stay in-plugin and get applied against
// the plugin's own sdk.Context before Build.
//
// A real plugin:
//
//  1. Reads ParentInfo (req.GetParentInfoJson(), JSON of pApi.ParentInfo)
//     to open sdk.NewStackReference against the parent stack.
//  2. Pulls previously-exported outputs (project id, connection
//     strings) via the StackReference.
//  3. Registers per-stack resources (DB user, random password) via its
//     own sdk.Context, applying AddDependency/AddOutput as needed.
//  4. Builds the response with the env vars / tpl extensions the
//     consuming stack needs.
func ComputeProcess(req *scpluginv1.ComputeProcessRequest) (*scpluginv1.ComputeProcessResponse, error) {
	resName := req.GetResourceName()
	resType := req.GetResourceType()
	stackName := req.GetHeader().GetStackName()

	return scPlugin.NewComputeResult().
		Urn(fmt.Sprintf("echo-compute:%s:%s", resType, resName)).
		EnvVar("ECHO_RESOURCE_NAME", resName, resType, resName, stackName).
		SecretEnvVar("ECHO_SECRET_TOKEN", "replace-me-with-real-cloud-output", resType, resName, stackName).
		ResourceTplExtension(resName, map[string]string{
			"echoResourceName": resName,
			"echoResourceType": resType,
		}).
		DependencyTplExtension("consumer", resName, map[string]string{
			"echoStackName": stackName,
		}).
		Build()
}
