package api

import (
	"context"
	"fmt"
	"slices"

	"github.com/LUKER2DW/sentinela/internal/camera/onvif"
)

// Camera ids are never reissued (store.SyncCameraIDs). A removed camera's
// id stays retired: the wizard and the settings editor skip it, the editor
// refuses it for a new camera, and so does scripts/add-camera.sh (through
// `sentinela check-camera-id`). A hand edit of config.yaml can still bring
// an id back: it is then a new camera, and the grants, recordings and
// events the old one left move to an archive id no camera can have, so
// they never attach to it.

// cameraSettingKeys are the settings stored per camera id.
func cameraSettingKeys(id string) []string {
	return []string{detectionKey(id), onvif.PullPointSettingKey(id)}
}

// SyncCameraIDs records the configured cameras and retires the ones that
// were removed, dropping their grants and settings. The configuration only
// changes through a restart (scripts/apply-config.sh), so serve calls it
// once at start; if that failed, retiredCameraIDs tries again before any
// new id is handed out.
func (s *Server) SyncCameraIDs(ctx context.Context) error {
	s.idsMu.Lock()
	defer s.idsMu.Unlock()
	if s.idsSynced {
		return nil
	}
	ids := make([]string, 0, len(s.Cfg.Cameras))
	for _, c := range s.Cfg.Cameras {
		ids = append(ids, c.ID)
	}
	_, removed, revived, err := s.Store.SyncCameraIDs(ctx, ids, cameraSettingKeys)
	if err != nil {
		return fmt.Errorf("camera ids: %w", err)
	}
	for _, id := range removed {
		s.Log.Info("camera removed: its id is retired and never given to another camera", "camera", id)
		_ = s.Store.Audit(ctx, 0, "", "camera.retire", id, true, "grants and settings dropped; recordings kept until retention")
	}
	for _, id := range revived {
		s.Log.Warn("a removed camera's id was configured again: it is a new camera; the old one's grants, recordings and events were moved to an archive id", "camera", id)
		_ = s.Store.Audit(ctx, 0, "", "camera.revive", id, true, "old grants, recordings and events archived; settings dropped")
	}
	if len(removed)+len(revived) > 0 {
		s.LiveMediaChanged() // narrowed grants apply to open live streams too
	}
	s.idsSynced = true
	return nil
}

// retiredCameraIDs lists the ids that must not be given to a new camera.
func (s *Server) retiredCameraIDs(ctx context.Context) ([]string, error) {
	if err := s.SyncCameraIDs(ctx); err != nil {
		return nil, err
	}
	ids, err := s.Store.RetiredCameraIDs(ctx)
	if err != nil {
		return nil, err
	}
	// Configured ids are in use, whatever the table says.
	return slices.DeleteFunc(ids, func(id string) bool { return s.Cfg.Camera(id) != nil }), nil
}

// freeCameraID returns the first "camN" from n on that is not taken.
func freeCameraID(n int, taken func(string) bool) string {
	for ; ; n++ {
		if id := fmt.Sprintf("cam%d", n); !taken(id) {
			return id
		}
	}
}
