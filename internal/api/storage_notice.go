package api

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
	"github.com/LUKER2DW/sentinela/internal/store"
)

// Protected clips deleted by the recorder's full-disk valve
// (recording.Retention) are announced to the admins. The recorder never
// depends on the API, and it cannot write the audit log (private database):
// it writes a row of the index's recorder_notices, and this watcher copies
// each new row into the audit log and turns it into a notice.

// valveNoticeKey keeps the id of the last recorder notice announced.
const valveNoticeKey = "notice.recorder_after"

// WatchStorageNotices checks for new valve rows every minute until ctx ends.
func (s *Server) WatchStorageNotices(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		s.raiseValveNotices(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// raiseValveNotices sends a notice for each valve row not announced yet.
// The first run only notes where the audit log is.
func (s *Server) raiseValveNotices(ctx context.Context) {
	after, err := strconv.ParseInt(s.Store.Setting(ctx, valveNoticeKey, ""), 10, 64)
	if err != nil {
		if last, err := s.Store.LastRecorderNoticeID(ctx); err == nil {
			_ = s.Store.SetSetting(ctx, valveNoticeKey, strconv.FormatInt(last, 10))
		}
		return
	}
	rows, err := s.Store.RecorderNoticesAfter(ctx, store.AuditProtectValve, after, 20)
	if err != nil || len(rows) == 0 {
		return
	}
	for _, r := range rows {
		var names []string
		for _, id := range strings.Split(r.Target, ",") {
			if c := s.Cfg.Camera(id); c != nil {
				id = c.Name
			}
			if id != "" {
				names = append(names, id)
			}
		}
		s.Log.Warn("full disk: protected recordings were deleted", "detail", r.Detail, "cameras", r.Target)
		_ = s.Store.Audit(ctx, 0, "", store.AuditProtectValve, r.Target, true, r.Detail)
		s.adminNotice(ctx, map[string]any{
			"title": "Disco cheio: trechos protegidos apagados",
			"body":  r.Detail + " (" + strings.Join(names, ", ") + "). Free space or lower what is kept.",
			"tag":   "sentinela-protect-valve",
			"url":   "/#/system",
		})
	}
	_ = s.Store.SetSetting(ctx, valveNoticeKey, strconv.FormatInt(rows[len(rows)-1].ID, 10))
}

// adminNotice sends msg to every active admin.
func (s *Server) adminNotice(ctx context.Context, msg map[string]any) {
	users, err := s.Store.ListUsers(ctx)
	if err != nil {
		return
	}
	for _, a := range users {
		if a.Role == auth.RoleAdmin && !a.Disabled && !a.Pending {
			s.sendAll(ctx, a.ID, msg)
		}
	}
}
