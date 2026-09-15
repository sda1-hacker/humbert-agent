package permission

import (
	"strings"
	"testing"
)

func TestBuildPresentationHidesWriteBody(t *testing.T) {
	secretBody := "very-sensitive-file-body"
	presentation, err := BuildPresentation(Request{
		AgentID: "a", SessionID: "s", ToolName: "write_file", Risk: RiskWrite,
		Arguments: `{"path":"notes.txt","overwrite":true,"content":"` + secretBody + `"}`,
		Identity: CapabilityIdentity{
			Version: CapabilityIdentityVersion, Kind: CapabilityBuiltin, Tool: "write_file", Risk: RiskWrite,
			SandboxFingerprint: "sbx1:test",
		},
	})
	if err != nil {
		t.Fatalf("构建 write_file 展示失败: %v", err)
	}
	joined := presentation.Title + presentation.Description
	for _, field := range presentation.Fields {
		joined += field.Label + field.Value
	}
	if strings.Contains(joined, secretBody) {
		t.Fatal("write_file 正文泄漏到 Approval Presentation")
	}
	if !strings.Contains(joined, "notes.txt") {
		t.Fatal("Approval Presentation 应展示目标文件")
	}
}

func TestBuildPresentationRedactsCommandSecrets(t *testing.T) {
	presentation, err := BuildPresentation(Request{
		AgentID: "a", SessionID: "s", ToolName: "run_command", Risk: RiskExec,
		Arguments: `{"command":"git","args":["-c","token=super-secret-value","status"]}`,
		Identity: CapabilityIdentity{
			Version: CapabilityIdentityVersion, Kind: CapabilityCommand, Tool: "run_command", Risk: RiskExec,
			SandboxFingerprint: "sbx1:test", Command: "git", Executable: "/usr/bin/git",
		},
	})
	if err != nil {
		t.Fatalf("构建 run_command 展示失败: %v", err)
	}
	joined := ""
	for _, field := range presentation.Fields {
		joined += field.Value
	}
	if strings.Contains(joined, "super-secret-value") {
		t.Fatal("command secret 泄漏到 Approval Presentation")
	}
	if !strings.Contains(joined, "[REDACTED]") {
		t.Fatal("敏感 command 参数应被统一日志脱敏器替换")
	}
	if !strings.Contains(joined, "/usr/bin/git") {
		t.Fatal("run_command 审批应展示绑定的 executable identity")
	}
}
