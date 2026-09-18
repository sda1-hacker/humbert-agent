package notifications

import (
	"context"
	"testing"
)

type fakeProvider struct {
	values []Notification
}

func (p *fakeProvider) Send(_ context.Context, value Notification) error {
	p.values = append(p.values, value)
	return nil
}

func TestServiceNormalizesNotification(t *testing.T) {
	provider := &fakeProvider{}
	service := New(provider)
	if err := service.Send(context.Background(), Notification{Title: "测试", Body: "内容"}); err != nil {
		t.Fatal(err)
	}
	if len(provider.values) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(provider.values))
	}
	value := provider.values[0]
	if value.ID == "" || value.CreatedAt == "" || value.Level != LevelInfo {
		t.Fatalf("notification was not normalized: %#v", value)
	}
}
