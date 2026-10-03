package app

import (
	"context"
	"errors"
	"github.com/sda1-hacker/humbert-agent/internal/component"
	"reflect"
	"testing"
)

// 后台生产者、正在使用资源的运行、资源释放之间的先后关系是关闭流程的关键约束。
func TestLifecycleStopsWorkBeforeRunsAndResources(t *testing.T) {
	var calls []string
	failure := errors.New("close failure")
	l := &lifecycle{}
	register := func(phase int, name string, err error) {
		l.add(phase, name, func(context.Context) error { calls = append(calls, name); return err })
	}
	register(closeResources, "database", failure)
	register(finishRuns, "runtime", nil)
	register(stopWork, "tasks", nil)
	register(closeResources, "module", nil)
	register(stopWork, "module-worker", nil)
	if err := l.close(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("lost cleanup error: %v", err)
	}
	if err := l.close(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("second close lost result: %v", err)
	}
	expected := []string{"module-worker", "tasks", "runtime", "module", "database"}
	if !reflect.DeepEqual(calls, expected) {
		t.Fatalf("cleanup order = %v, want %v", calls, expected)
	}
}

func TestModuleFailedStartStopsPartialWorkAndClosesUnstartedModules(t *testing.T) {
	ctx := context.Background()
	var calls []string
	failure := errors.New("start failure")
	makeModule := func(id string, fail bool) component.Installer {
		return func(context.Context, component.Host) (component.Module, error) {
			return component.Module{
				ID: id,
				Start: func(context.Context) error {
					calls = append(calls, "start:"+id)
					if fail {
						return failure
					}
					return nil
				},
				Stop:  func(context.Context) error { calls = append(calls, "stop:"+id); return nil },
				Close: func(context.Context) error { calls = append(calls, "close:"+id); return nil },
			}, nil
		}
	}
	manager := &moduleManager{}
	if err := manager.install(ctx, component.Host{}, nil, []component.Installer{makeModule("first", false), makeModule("second", true), makeModule("third", false)}); err != nil {
		t.Fatal(err)
	}
	if err := manager.start(ctx); !errors.Is(err, failure) {
		t.Fatalf("start error: %v", err)
	}
	l := &lifecycle{}
	l.add(stopWork, "modules", manager.stop)
	l.add(closeResources, "modules", manager.close)
	if err := l.close(ctx); err != nil {
		t.Fatal(err)
	}
	expected := []string{"start:first", "start:second", "stop:second", "stop:first", "close:third", "close:second", "close:first"}
	if !reflect.DeepEqual(calls, expected) {
		t.Fatalf("rollback calls = %v, want %v", calls, expected)
	}
}

func TestModuleInvalidRegistrationReleasesBothOwners(t *testing.T) {
	var closed []string
	makeModule := func(label string) component.Installer {
		return func(context.Context, component.Host) (component.Module, error) {
			return component.Module{ID: "duplicate", Close: func(context.Context) error { closed = append(closed, label); return nil }}, nil
		}
	}
	manager := &moduleManager{}
	if err := manager.install(context.Background(), component.Host{}, nil, []component.Installer{makeModule("owned"), makeModule("rejected")}); err == nil {
		t.Fatal("duplicate module accepted")
	}
	if err := manager.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(closed, []string{"rejected", "owned"}) {
		t.Fatalf("closed = %v", closed)
	}
}
