// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package echo

import (
	"testing"

	. "github.com/onsi/gomega"

	scpluginv1 "github.com/simple-container-com/api/pkg/plugin/proto/v1"
)

// TestComputeProcess_BuildsTheFullSurface — the shape test: every
// ComputeResultBuilder method the template demonstrates must land in
// the response. If an author copies the echo stub and strips one of
// the four classes (plain env, secret env, resource tpl, dependency
// tpl), this test flags it before SC core sees the gap.
func TestComputeProcess_BuildsTheFullSurface(t *testing.T) {
	RegisterTestingT(t)

	resp, err := ComputeProcess(&scpluginv1.ComputeProcessRequest{
		Header:       &scpluginv1.RequestHeader{StackName: "backend-prod"},
		ResourceType: "echo-bucket",
		ResourceName: "my-bucket",
	})
	Expect(err).ToNot(HaveOccurred())

	// The URN is set so the SC-core sidecar can mint a ResourceOutput ref.
	Expect(resp.GetUrn()).To(ContainSubstring("my-bucket"))

	// Two env vars — one plain, one secret.
	Expect(resp.GetEnvVars()).To(HaveLen(2))
	Expect(resp.GetEnvVars()[0].GetSecret()).To(BeFalse())
	Expect(resp.GetEnvVars()[0].GetName()).To(Equal("ECHO_RESOURCE_NAME"))
	Expect(resp.GetEnvVars()[0].GetValue()).To(Equal("my-bucket"))
	Expect(resp.GetEnvVars()[0].GetStackName()).To(Equal("backend-prod"))

	Expect(resp.GetEnvVars()[1].GetSecret()).To(BeTrue())
	Expect(resp.GetEnvVars()[1].GetName()).To(Equal("ECHO_SECRET_TOKEN"))

	// Two tpl extensions — one resource, one dependency.
	Expect(resp.GetTplExtensions()).To(HaveLen(2))
	Expect(resp.GetTplExtensions()[0].GetKind()).To(Equal(scpluginv1.TemplateExtensionKind_TEMPLATE_EXTENSION_KIND_RESOURCE))
	Expect(resp.GetTplExtensions()[0].GetResName()).To(Equal("my-bucket"))
	Expect(resp.GetTplExtensions()[0].GetValues()).To(HaveKeyWithValue("echoResourceName", "my-bucket"))
	Expect(resp.GetTplExtensions()[0].GetValues()).To(HaveKeyWithValue("echoResourceType", "echo-bucket"))

	Expect(resp.GetTplExtensions()[1].GetKind()).To(Equal(scpluginv1.TemplateExtensionKind_TEMPLATE_EXTENSION_KIND_DEPENDENCY))
	Expect(resp.GetTplExtensions()[1].GetDepName()).To(Equal("consumer"))
	Expect(resp.GetTplExtensions()[1].GetResName()).To(Equal("my-bucket"))
}
