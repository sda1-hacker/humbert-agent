package services

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/skills"
)

// 匿名嵌入只收敛 Go 定义；WebView 仍收到原有顶层字段和 null/空数组协议。
func TestSkillDTOsKeepFlatJSONContractAndOwnMetadata(t *testing.T) {
	info := skills.Info{
		Name: "demo", Description: "描述", SpecStatus: skills.SpecStatus("valid"),
		RuntimeStatus: skills.RuntimeStatus("ready"), RootDir: "/skills/demo", Identity: "hash",
		FileCount: 2, SizeBytes: 32, HasAssets: true, Valid: true, DirectoryName: "demo",
		Metadata:  map[string]string{"author": "Alice"},
		UpdatedAt: time.Date(2026, 10, 5, 8, 0, 0, 123, time.FixedZone("CN", 8*3600)),
	}
	list := projectSkillDTO(info)
	detail := SkillDetailDTO{SkillMetadataDTO: projectSkillMetadata(info), Files: projectSkillFiles(nil)}
	common := `{"name":"demo","description":"描述","specStatus":"valid","runtimeStatus":"ready","rootDir":"/skills/demo","identity":"hash","fileCount":2,"sizeBytes":32,"hasAssets":true,"updatedAt":"2026-10-05T00:00:00.000000123Z","metadata":{"author":"Alice"},"source":{"known":false,"drifted":false}}`
	for _, test := range []struct {
		name  string
		value any
		extra map[string]any
	}{
		{"列表", list, map[string]any{"directoryName": "demo", "valid": true, "hasReferences": false, "hasScripts": false, "usedByAgents": nil}},
		{"详情", detail, map[string]any{"files": []any{}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var want, got map[string]any
			if err := json.Unmarshal([]byte(common), &want); err != nil {
				t.Fatal(err)
			}
			for key, value := range test.extra {
				want[key] = value
			}
			data, err := json.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("JSON 字段发生变化: got=%s want=%v", data, want)
			}
		})
	}
	list.Metadata["author"] = "changed"
	if info.Metadata["author"] != "Alice" || detail.Metadata["author"] != "Alice" {
		t.Fatal("不同展示结果共享了可变 Metadata")
	}
}

// 一个 Agent 的重复引用不重复显示；使用列表复用一次排序，并保留最小身份投影。
func TestSkillUsageKeepsStableOrderAndUniqueAgentReferences(t *testing.T) {
	values := []agents.AgentInfo{
		{Agent: agents.Agent{ID: "b", Name: " Z ", EnabledSkills: []string{"demo", " demo ", ""}}},
		{Agent: agents.Agent{ID: "c", Name: "A", EnabledSkills: []string{"demo"}}},
		{Agent: agents.Agent{ID: "a", Name: "A", EnabledSkills: []string{"demo", "other"}}},
		{Agent: agents.Agent{Name: "无 ID", EnabledSkills: []string{"demo"}}},
	}
	projected := projectSkillAgents(values)
	usage := buildSkillUsageMap(projected)
	want := []SkillAgentDTO{{ID: "a", Name: "A"}, {ID: "c", Name: "A"}, {ID: "b", Name: "Z"}}
	if !reflect.DeepEqual(usage["demo"], want) || len(usage["other"]) != 1 {
		t.Fatalf("引用顺序/去重错误: %#v", usage)
	}
	projected[0].EnabledSkills[0] = "changed"
	if values[2].Agent.EnabledSkills[0] != "demo" || usage["demo"][0].EnabledSkills != nil {
		t.Fatal("投影应复制启用集合，使用列表仅包含身份")
	}
}
