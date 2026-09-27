package searchindex

import (
	"context"
	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
	"strings"
)

// RefreshSessions 只重建 revision 改变的会话；JSONL 始终是权威数据。
func RefreshSessions(ctx context.Context, index *Index, agentService *agents.Service, sessionService *sessions.Service) error {
	agents, err := agentService.List(ctx)
	if err != nil {
		return err
	}
	keep := make(map[string]bool)
	for _, agent := range agents {
		sessions, err := sessionService.List(ctx, agent.Agent.ID)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			keep[session.ID] = true
			revision := session.UpdatedAt.UnixNano()
			cached, exists, err := index.CachedSession(ctx, session.ID)
			if err != nil {
				return err
			}
			indexed := Session{ID: session.ID, AgentID: session.AgentID, Title: session.Title, Archived: session.Archived, Revision: revision}
			if exists && cached.Revision == revision {
				if cached.Title != indexed.Title || cached.Archived != indexed.Archived || cached.AgentID != indexed.AgentID {
					if err := index.UpdateSessionMetadata(ctx, indexed); err != nil {
						return err
					}
				}
				continue
			}
			if err := index.ReplaceStream(ctx, indexed, func(add func(Message) error) error {
				var insertErr error
				visitErr := sessionService.VisitActiveBranchReverse(ctx, session.ID, func(entry transcript.Entry) bool {
					if entry.Type != transcript.EntryMessage || entry.Message == nil {
						return true
					}
					role := entry.Message.Role
					if role != transcript.RoleUser && role != transcript.RoleAssistant {
						return true
					}
					var content strings.Builder
					for _, block := range entry.Message.Content {
						if block.Type == transcript.ContentText {
							content.WriteString(block.Text)
							content.WriteByte('\n')
						}
						if block.Type == transcript.ContentFile {
							content.WriteString(block.ExtractedText)
							content.WriteByte('\n')
						}
					}
					insertErr = add(Message{EntryID: entry.ID, Role: string(role), Timestamp: entry.Timestamp, Content: content.String()})
					return insertErr == nil
				})
				if visitErr != nil {
					return visitErr
				}
				return insertErr
			}); err != nil {
				return err
			}
		}
	}
	if err := index.Prune(ctx, keep); err != nil {
		return err
	}
	return nil
}
