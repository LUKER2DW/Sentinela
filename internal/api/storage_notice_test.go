package api

import (
	"context"
	"strings"
	"testing"

	"github.com/LUKER2DW/sentinela/internal/store"
)

// The recorder's full-disk valve writes an audit row; the API announces each
// new one to the active admins, once.
func TestValveNoticeReachesAdmins(t *testing.T) {
	_, srv := newTestServer(t)
	ctx := context.Background()
	admin, _ := srv.Store.CreateUser(ctx, "admin", "hash", "admin")
	viewer, _ := srv.Store.CreateUser(ctx, "kid", "hash", "viewer")
	toAdmin, toViewer := srv.push.subscribe(admin), srv.push.subscribe(viewer)
	defer srv.push.unsubscribe(toAdmin)
	defer srv.push.unsubscribe(toViewer)
	// A row from before the first run is history, not news.
	_ = srv.Store.RecorderNotice(ctx, store.AuditProtectValve, "cam1", "old")
	srv.raiseValveNotices(ctx)
	if len(toAdmin) != 0 {
		t.Fatal("history was announced")
	}
	_ = srv.Store.RecorderNotice(ctx, store.AuditProtectValve, "cam1", "3 protected clips deleted")
	srv.raiseValveNotices(ctx)
	select {
	case msg := <-toAdmin:
		if !strings.Contains(string(msg), "sentinela-protect-valve") || !strings.Contains(string(msg), "Frente") {
			t.Fatalf("notice: %s", msg)
		}
	default:
		t.Fatal("the admin was not told")
	}
	if len(toViewer) != 0 {
		t.Fatal("a viewer was told")
	}
	srv.raiseValveNotices(ctx)
	if len(toAdmin) != 0 {
		t.Fatal("announced twice")
	}
}
