package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth/redact"
	"github.com/LUKER2DW/sentinela/internal/camera/dvrip"
	"github.com/LUKER2DW/sentinela/internal/camera/onvif"
	"github.com/LUKER2DW/sentinela/internal/config"
)

type cameraDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Recording   bool   `json:"recording"`
	RecordAudio bool   `json:"record_audio"`
	Retention   int    `json:"retention_days"`
	// Control: PTZ, presets, light and day/night (DVRIP cameras only).
	Control bool   `json:"control"`
	Vendor  string `json:"vendor"`
}

func (s *Server) listCameras(w http.ResponseWriter, r *http.Request) {
	out := make([]cameraDTO, 0, len(s.Cfg.Cameras))
	for _, c := range s.Cfg.Cameras {
		if !s.canSee(r, c.ID) {
			continue
		}
		out = append(out, cameraDTO{ID: c.ID, Name: c.Name, Recording: c.Recording(), RecordAudio: c.AudioRecording(), Retention: c.RetentionDays,
			Control: c.HasDVRIP(), Vendor: c.Vendor})
	}
	writeJSON(w, http.StatusOK, out)
}

// cameraCtrl is s.camera for DVRIP control routes: cameras of other
// vendors answer 501 (view and record only).
func (s *Server) cameraCtrl(w http.ResponseWriter, r *http.Request) (*config.Camera, *dvrip.Controller) {
	cam := s.camera(w, r)
	if cam == nil {
		return nil, nil
	}
	ctrl := s.ctrl[cam.ID]
	if ctrl == nil {
		writeErr(w, http.StatusNotImplemented, "this camera has no remote control (vendor "+cam.Vendor+")")
		return nil, nil
	}
	return cam, ctrl
}

// camErr converts a driver error into a safe message and HTTP status.
func (s *Server) camErr(w http.ResponseWriter, cam *config.Camera, err error) {
	msg := redact.Secrets(err.Error(), cam.Password)
	s.Log.Warn("camera control failed", "camera", cam.ID, "err", msg)
	switch {
	case errors.Is(err, dvrip.ErrInvalidCommand):
		writeErr(w, http.StatusBadRequest, "invalid command")
	case errors.Is(err, dvrip.ErrUnsupportedModel):
		writeErr(w, http.StatusForbidden, "control not approved for this camera model")
	case errors.Is(err, dvrip.ErrRejected):
		writeErr(w, http.StatusBadGateway, "camera rejected the command")
	case errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusGatewayTimeout, "camera did not answer in time")
	default:
		writeErr(w, http.StatusBadGateway, "camera unreachable")
	}
}

func (s *Server) cameraControl(w http.ResponseWriter, r *http.Request) {
	cam, ctrl := s.cameraCtrl(w, r)
	if cam == nil {
		return
	}
	s.stateMu.Lock()
	c, ok := s.stateTTL[cam.ID]
	s.stateMu.Unlock()
	if ok && time.Since(c.at) < 15*time.Second && r.URL.Query().Get("fresh") == "" {
		writeJSON(w, http.StatusOK, c.st)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	st, err := ctrl.State(ctx)
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	s.stateMu.Lock()
	s.stateTTL[cam.ID] = cachedState{at: time.Now(), st: st}
	s.stateMu.Unlock()
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) invalidateState(id string) {
	s.stateMu.Lock()
	delete(s.stateTTL, id)
	s.stateMu.Unlock()
}

func (s *Server) ptz(w http.ResponseWriter, r *http.Request) {
	cam, ctrl := s.cameraCtrl(w, r)
	if cam == nil {
		return
	}
	var body struct {
		Action string `json:"action"` // start (also keepalive), stop, nudge
		Op     string `json:"op"`     // move, zoom, focus, iris
		Dir    string `json:"dir"`
		Speed  int    `json:"speed"`
		Ms     int    `json:"ms"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dvrip.DefaultTimeout+time.Second)
	defer cancel()
	var err error
	if body.Action != "stop" {
		s.takeControl(r, cam.ID)
	}
	s.ptzQuiet.hold(cam.ID, ptzSettle)
	switch body.Action {
	case "start":
		err = ctrl.Start(ctx, body.Op, body.Dir, body.Speed)
	case "stop":
		// Stop must go through even if the client went away.
		ctx2, cancel2 := context.WithTimeout(context.WithoutCancel(r.Context()), dvrip.DefaultTimeout)
		defer cancel2()
		err = ctrl.Stop(ctx2)
	case "nudge":
		err = ctrl.Nudge(ctx, body.Op, body.Dir, body.Speed, time.Duration(body.Ms)*time.Millisecond)
	default:
		err = dvrip.ErrInvalidCommand
	}
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) preset(w http.ResponseWriter, r *http.Request) {
	cam, ctrl := s.cameraCtrl(w, r)
	if cam == nil {
		return
	}
	id, err := strconv.Atoi(r.PathValue("preset"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad preset id")
		return
	}
	var body struct {
		Action string `json:"action"` // goto, set, clear
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.takeControl(r, cam.ID)
	ctx, cancel := context.WithTimeout(r.Context(), 2*dvrip.DefaultTimeout)
	defer cancel()
	if body.Action == "goto" {
		s.ptzQuiet.hold(cam.ID, presetSettle)
	}
	err = ctrl.Preset(ctx, body.Action, id)
	if body.Action != "goto" {
		s.audit(r, "camera.preset."+body.Action, fmt.Sprintf("%s#%d", cam.ID, id), err == nil, "")
		s.invalidateState(cam.ID)
	}
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) setLight(w http.ResponseWriter, r *http.Request) {
	cam, ctrl := s.cameraCtrl(w, r)
	if cam == nil {
		return
	}
	var body struct {
		On bool `json:"on"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*dvrip.DefaultTimeout)
	defer cancel()
	err := ctrl.SetWhiteLight(ctx, body.On)
	s.audit(r, "camera.white_light", cam.ID, err == nil, strconv.FormatBool(body.On))
	s.invalidateState(cam.ID)
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) setDayNight(w http.ResponseWriter, r *http.Request) {
	cam, ctrl := s.cameraCtrl(w, r)
	if cam == nil {
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*dvrip.DefaultTimeout)
	defer cancel()
	err := ctrl.SetDayNight(ctx, body.Mode)
	s.audit(r, "camera.day_night", cam.ID, err == nil, body.Mode)
	s.invalidateState(cam.ID)
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// cameraClock reports how far the camera's clock (the one burned into the
// video) is from the server's NTP-synced clock. Read-only ONVIF call.
func (s *Server) cameraClock(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	c := onvif.NewClient(cam.Host, cam.ONVIFPort, cam.User, cam.Password)
	t0 := time.Now()
	camTime, err := c.SystemTime(ctx)
	t1 := time.Now()
	if err != nil {
		s.Log.Warn("camera clock read failed", "camera", cam.ID, "err", redact.Secrets(err.Error(), cam.Password))
		writeErr(w, http.StatusBadGateway, "camera did not report its time")
		return
	}
	mid := t0.Add(t1.Sub(t0) / 2)
	// The camera reports whole seconds (truncated): on average its real time
	// is half a second later, so the offset is accurate to about ±0.5 s.
	writeJSON(w, http.StatusOK, map[string]any{
		"camera":    camTime,
		"server":    mid.UTC(),
		"offset_ms": camTime.Add(500 * time.Millisecond).Sub(mid).Milliseconds(),
	})
}
